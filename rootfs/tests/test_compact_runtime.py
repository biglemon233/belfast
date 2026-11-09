"""验证裁剪后动态库可运行，且不会改写 uv 缓存或越过软链接。

Verifies that compacted libraries run without modifying uv caches or traversing symlinks.
"""

import ctypes
import importlib.util
import os
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location(
    "compact_runtime", Path(__file__).parents[1] / "build/compact-runtime.py")
compact_runtime = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(compact_runtime)


@unittest.skipUnless(os.name == "posix" and all(shutil.which(tool) for tool in ("cc", "strip", "readelf")),
                     "Requires a native ELF compiler and binutils")
class CompactRuntimeTest(unittest.TestCase):
    """使用真实 ELF 测试导出符号和缓存隔离。 / Tests real ELF exports and cache isolation."""

    def test_managed_interpreter_distribution_is_preserved(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root = base / "rootfs"
            (root / "opt/azurpilot/.venv/bin").mkdir(parents=True)
            distribution = root / "opt/uv-python/cpython-3.14.6-linux-x86_64-gnu"
            interpreter = distribution / "bin/python3.14"
            library = distribution / "lib/libpython3.14.so.1.0"
            extension = distribution / "lib/python3.14/lib-dynload/_ssl.so"
            source = base / "source.c"
            source.write_text("int main(void) { return 0; }\n")
            for target in (interpreter, library, extension):
                target.parent.mkdir(parents=True, exist_ok=True)
                subprocess.run(["cc", "-g", str(source), "-o", str(target)], check=True)
            os.symlink(interpreter, root / "opt/azurpilot/.venv/bin/python")
            originals = {target: target.read_bytes() for target in (interpreter, library, extension)}
            with mock.patch.object(compact_runtime.subprocess, "run", wraps=subprocess.run) as run:
                report = compact_runtime.compact(root)
            self.assertEqual(report["compacted_files"], 0)
            self.assertEqual(report["saved_bytes"], 0)
            self.assertFalse(run.called)
            for target, original in originals.items():
                self.assertEqual(target.read_bytes(), original)
            subprocess.run([str(root / "opt/azurpilot/.venv/bin/python")], check=True)

    def test_library_runs_and_cache_and_symlinks_are_preserved(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root = base / "rootfs"
            package = root / "opt/azurpilot/.venv/lib"
            package.mkdir(parents=True)
            source = base / "source.c"
            source.write_text("int ocr_fixture(int value) { return value * 7; }\n")
            cache = base / "cache.so"
            subprocess.run(["cc", "-shared", "-fPIC", "-g", str(source), "-o", str(cache)], check=True)
            original = cache.read_bytes()
            original_mode = cache.stat().st_mode & 0o777
            target = package / "fixture.so"
            os.link(cache, target)
            os.symlink(cache, package / "external.so")
            os.symlink(base, package / "external_directory")
            report = compact_runtime.compact(root)
            self.assertGreater(report["saved_bytes"], 0)
            self.assertEqual(report["compacted_files"], 1)
            self.assertEqual(cache.read_bytes(), original)
            self.assertEqual(target.stat().st_mode & 0o777, original_mode)
            self.assertNotEqual(cache.stat().st_ino, target.stat().st_ino)
            self.assertTrue((package / "external.so").is_symlink())
            library = ctypes.CDLL(str(target))
            self.assertEqual(library.ocr_fixture(6), 42)

    def test_rejects_python_directory_outside_rootfs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "rootfs"
            (root / "opt/azurpilot").mkdir(parents=True)
            os.symlink(temporary, root / "opt/uv-python")
            with self.assertRaises(ValueError):
                compact_runtime.compact(root)

    def test_foreign_android_cache_is_preserved_without_calling_strip(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root = base / "rootfs"
            package = root / "opt/azurpilot/.venv/lib"
            package.mkdir(parents=True)
            source = base / "source.c"
            source.write_text("int ocr_fixture(int value) { return value * 7; }\n")
            target = package / "fixture.so"
            subprocess.run(["cc", "-shared", "-fPIC", "-g", str(source), "-o", str(target)], check=True)
            foreign = package / "uiautomator2cache/cache/minicap.so-010087d6d0/minicap.so"
            foreign.parent.mkdir(parents=True)
            header = bytearray(target.read_bytes())
            endian = "little" if header[5] == 1 else "big"
            machine = int.from_bytes(header[18:20], endian)
            header[18:20] = (40 if machine != 40 else 62).to_bytes(2, endian)
            foreign.write_bytes(header)
            with mock.patch.object(compact_runtime.subprocess, "run", wraps=subprocess.run) as run:
                report = compact_runtime.compact(root)
            self.assertEqual(report["elf_files"], 2)
            self.assertEqual(report["skipped_foreign_elf_files"], 1)
            self.assertEqual(report["compacted_files"], 1)
            self.assertEqual(foreign.read_bytes(), header)
            strip_calls = [call for call in run.call_args_list if "--strip-unneeded" in call.args[0]]
            self.assertEqual(len(strip_calls), 1)
            self.assertNotIn("uiautomator2cache", strip_calls[0].args[0][-1])

    def test_dynamic_symbols_allow_section_renumbering_but_reject_export_changes(self):
        before = b" 1: 0000000000001234 16 FUNC GLOBAL DEFAULT 7 ocr_fixture@@OCR_1\n"
        after = before.replace(b"DEFAULT 7", b"DEFAULT 6")
        sections_before = b" [ 7] .text PROGBITS\n"
        sections_after = b" [ 6] .text PROGBITS\n"
        parse = compact_runtime.parse_dynamic_symbols
        expected = parse(before, sections_before)
        self.assertEqual(expected, parse(after, sections_after))
        for old, new in ((b"1234", b"1235"), (b"16 FUNC", b"17 FUNC"),
                         (b"FUNC", b"OBJECT"), (b"GLOBAL", b"WEAK"),
                         (b"DEFAULT", b"HIDDEN"), (b"ocr_fixture", b"different_export"),
                         (b"OCR_1", b"OCR_2")):
            self.assertNotEqual(expected, parse(after.replace(old, new), sections_after))
        self.assertNotEqual(expected, parse(after, sections_after.replace(b".text", b".data")))
        with self.assertRaises(ValueError):
            parse(after, sections_before)

    def test_rejected_strip_output_keeps_original_library_and_removes_temporary_file(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root = base / "rootfs"
            package = root / "opt/azurpilot/.venv/lib"
            package.mkdir(parents=True)
            source = base / "source.c"
            source.write_text("int ocr_fixture(int value) { return value * 7; }\n")
            target = package / "fixture.so"
            subprocess.run(["cc", "-shared", "-fPIC", "-g", str(source), "-o", str(target)], check=True)
            original = target.read_bytes()
            with mock.patch.object(compact_runtime, "dynamic_symbols", side_effect=[(b"original",), (b"changed",)]):
                report = compact_runtime.compact(root)
            self.assertEqual(report["skipped_symbol_changes"], 1)
            self.assertEqual(report["compacted_files"], 0)
            self.assertEqual(target.read_bytes(), original)
            self.assertFalse(list(package.glob(".strip-*")))
            self.assertEqual(ctypes.CDLL(str(target)).ocr_fixture(6), 42)


    def test_misaligned_strip_output_keeps_loadable_original(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root = base / "rootfs"
            package = root / "opt/azurpilot/.venv/lib"
            package.mkdir(parents=True)
            source = base / "source.c"
            source.write_text("int ocr_fixture(int value) { return value * 7; }\n")
            target = package / "fixture.so"
            subprocess.run(["cc", "-shared", "-fPIC", "-g", str(source), "-o", str(target)], check=True)
            original = target.read_bytes()
            real_run = subprocess.run

            def corrupt_offset(command, **kwargs):
                result = real_run(command, **kwargs)
                if "--strip-unneeded" not in command:
                    return result
                candidate = Path(command[-1])
                content = bytearray(candidate.read_bytes())
                endian = "<" if content[5] == 1 else ">"
                if content[4] == 2:
                    offset = struct.unpack_from(endian + "Q", content, 32)[0]
                    entry_size, count = struct.unpack_from(endian + "HH", content, 54)
                    file_offset, integer = 8, "Q"
                else:
                    offset = struct.unpack_from(endian + "I", content, 28)[0]
                    entry_size, count = struct.unpack_from(endian + "HH", content, 42)
                    file_offset, integer = 4, "I"
                for index in range(count):
                    position = offset + index * entry_size
                    if struct.unpack_from(endian + "I", content, position)[0] == 1:
                        current = struct.unpack_from(endian + integer, content, position + file_offset)[0]
                        struct.pack_into(endian + integer, content, position + file_offset, current + 1)
                        break
                candidate.write_bytes(content)
                return result

            with mock.patch.object(compact_runtime.subprocess, "run", side_effect=corrupt_offset):
                report = compact_runtime.compact(root)
            self.assertEqual(report["skipped_segment_changes"], 1)
            self.assertEqual(report["compacted_files"], 0)
            self.assertEqual(target.read_bytes(), original)
            self.assertFalse(list(package.glob(".strip-*")))
            self.assertEqual(ctypes.CDLL(str(target)).ocr_fixture(6), 42)

    def test_empty_load_segments_must_keep_page_alignment(self):
        segments = ((0x170000, 0x160000, 0x2000, 0x2000, 6, 0x10000),
                    (0x180000, 0x170000, 0, 0, 6, 0x10000))
        compatible = compact_runtime.compatible_load_segments
        self.assertTrue(compatible(segments, segments))
        self.assertTrue(compatible(segments, segments[:1]))
        damaged = (segments[0], (0x180000, 0x1627bc, 0, 0, 6, 0x10000))
        self.assertFalse(compatible(segments, damaged))
        weakened = ((0x170000, 0x160000, 0x2000, 0x2000, 6, 0x1000),)
        self.assertFalse(compatible(segments, weakened))


if __name__ == "__main__":
    unittest.main()

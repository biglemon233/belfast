#!/usr/bin/env python3
"""In-place DT_NEEDED string shortening for AArch64 ELFs (Spike A tooling).

Why: a library file placed into APK `lib/<abi>/` only survives (a) AGP packaging and
(b) install-time extraction into nativeLibraryDir when its name matches `lib*.so`
(AGP drops other names; the AOSP installer keeps them for debuggable apps only).
Termux builds reference versioned SONAMEs (libtalloc.so.2, libbusybox.so.1.38.0),
so the DT_NEEDED strings are rewritten to `lib*.so` names and the files are renamed
to match.  Replacing a dynstr entry with a same-or-shorter string + NUL padding keeps
all other dynstr offsets valid, so no ELF headers need to be rebuilt.

Usage:
    python patch-dynstr.py <elf-file> <old-name> <new-name>

Exit code 0 on success; refuses if the new name is longer or the old string is not
present exactly once.
"""
import sys


def main() -> int:
    if len(sys.argv) != 4:
        print(__doc__)
        return 2
    path, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
    old_bytes = old.encode() + b"\0"
    new_bytes = new.encode()
    if len(new_bytes) > len(old_bytes) - 1:
        print(f"ERROR: new name '{new}' does not fit in {len(old_bytes) - 1} bytes")
        return 1
    with open(path, "rb") as f:
        data = f.read()
    count = data.count(old_bytes)
    if count != 1:
        print(f"ERROR: expected exactly 1 occurrence of '{old}\\0', found {count}")
        return 1
    padded = new_bytes + b"\0" * (len(old_bytes) - len(new_bytes))
    with open(path, "wb") as f:
        f.write(data.replace(old_bytes, padded))
    print(f"OK {path}: '{old}' -> '{new}' (pad {len(old_bytes) - len(new_bytes)} NUL)")
    return 0


if __name__ == "__main__":
    sys.exit(main())

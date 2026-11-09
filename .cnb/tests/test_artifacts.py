"""验证 CNB 产物来源、签名分流和应用版本兼容性。

Verifies CNB artifact provenance, signing routes, and app-version compatibility.
"""

from contextlib import redirect_stdout
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
from urllib.error import HTTPError

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))
import artifacts


class ArtifactTest(unittest.TestCase):
    """覆盖错误产物可能进入国内下载页的情况。 / Guards against incorrect download artifacts."""

    def setUp(self):
        """准备两架构的小型产物。 / Prepares small fixtures for both architectures."""
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.root = self.base / "runtime"
        self.dist = self.base / "dist"
        self.dist.mkdir()
        self.env = {
            "CNB_COMMIT": "a" * 40, "AZURPILOT_REF": "b" * 40,
            "CNB_REPO_SLUG": "azurpilot/AzurPilot-for-Android",
            "CNB_WEB_ENDPOINT": "https://cnb.cool", "RELEASE_TAG": "cnb-android-build-123",
            "SIGNING_READY": "true", "APP_VERSION_CODE": "1800000000",
            "APP_VERSION_NAME": "1.3.123", "APP_COMMIT": "c" * 40,
        }
        for abi in artifacts.ABIS:
            directory = self.root / abi
            directory.mkdir(parents=True)
            manifest = {
                "runtime": "azurpilot-android", "rootfs_arch": abi,
                "azurpilot_commit": self.env["AZURPILOT_REF"],
                "android_host_commit": self.env["CNB_COMMIT"], "rootfs_version": "runtime-123",
            }
            (directory / "BUILD_MANIFEST").write_text(json.dumps(manifest), encoding="utf-8")
            (directory / "rootfs.tar.xz").write_bytes(abi.encode())
            (self.dist / f"azurpilot-full-{abi}.apk").write_bytes(f"apk-{abi}".encode())
        (self.dist / "azurpilot-update.apk").write_bytes(b"slim-apk")
        (self.dist / "apk-version.json").write_text(json.dumps({
            "versionCode": 1800000000, "versionName": "1.3.123",
        }), encoding="utf-8")
        frontend = f"frontend-{self.env['AZURPILOT_REF']}.tar.xz"
        (self.root / artifacts.ABIS[0] / frontend).write_bytes(b"frontend")
        (self.root / artifacts.ABIS[0] / f"{frontend}.sha256").write_text("checksum", encoding="utf-8")

    def test_signed_downloads_and_checksums(self):
        """正式清单使用 CNB 并保留旧版兼容字段。 / Keeps CNB URLs and legacy manifest fields."""
        manifest = artifacts.prepare_release(self.root, self.dist, self.env)
        self.assertEqual(manifest["rootfsUrl"], manifest["runtimes"]["arm64-v8a"]["url"])
        self.assertEqual(manifest["rootfsSha256"], manifest["runtimes"]["arm64-v8a"]["sha256"])
        self.assertTrue(manifest["apkUrl"].startswith(
            "https://cnb.cool/azurpilot/AzurPilot-for-Android/-/releases/download/cnb-android-build-123/"))
        output = self.dist / "release"
        lines = (output / "SHA256SUMS").read_text(encoding="utf-8").splitlines()
        self.assertEqual(len(lines), len(list(output.iterdir())) - 1)
        for line in lines:
            checksum, filename = line.split("  ", 1)
            self.assertEqual(checksum, artifacts.digest(output / filename))

    def test_debug_artifacts_cannot_become_update_index(self):
        """调试包不生成正式更新清单。 / Debug artifacts cannot become a release update index."""
        self.env["SIGNING_READY"] = "false"
        manifest = artifacts.prepare_release(self.root, self.dist, self.env)
        self.assertEqual(manifest["signing"], "debug")
        self.assertNotIn("apkUrl", manifest)
        self.assertTrue(manifest["apk"]["file"].endswith("-debug.apk"))
        self.assertFalse((self.dist / "release/latest.json").exists())
        self.assertTrue((self.dist / "release/build-info.json").exists())

    def test_wrong_abi_host_or_upstream_is_rejected(self):
        """来源不一致时不能发布。 / Rejects mismatched runtime provenance."""
        path = self.root / "x86_64/BUILD_MANIFEST"
        original = json.loads(path.read_text(encoding="utf-8"))
        for key in ("rootfs_arch", "android_host_commit", "azurpilot_commit"):
            with self.subTest(key=key):
                path.write_text(json.dumps({**original, key: "wrong"}), encoding="utf-8")
                with self.assertRaisesRegex(ValueError, "provenance"):
                    artifacts.prepare_release(self.root, self.dist, self.env)
        self.assertFalse((self.dist / "release").exists())

    def test_version_mismatch_is_rejected(self):
        """版本元数据必须与构建输入一致。 / Rejects mismatched APK version metadata."""
        self.env["APP_VERSION_CODE"] = "1800000001"
        with self.assertRaisesRegex(ValueError, "version"):
            artifacts.prepare_release(self.root, self.dist, self.env)

    def test_incomplete_signing_and_reporting_secrets_fail_early(self):
        """不完整私密输入不能降级发布。 / Partial credentials cannot silently downgrade releases."""
        self.assertFalse(artifacts.signing_ready({}))
        for keys in ((artifacts.SIGNING_KEYS[0],), artifacts.SIGNING_KEYS,
                     (artifacts.REPORT_KEYS[0],)):
            with self.subTest(keys=keys), self.assertRaises(ValueError):
                artifacts.signing_ready(dict.fromkeys(keys, "present"))
        self.assertTrue(artifacts.signing_ready(dict.fromkeys(
            artifacts.SIGNING_KEYS + artifacts.REPORT_KEYS, "present")))

    def test_only_missing_release_is_a_first_build(self):
        """服务或权限故障不能伪装成首次发布。 / Distinguishes absence from service or auth failures."""
        with patch.object(artifacts, "urlopen", side_effect=HTTPError("url", 404, "missing", {}, None)):
            self.assertEqual(artifacts.previous_release("https://cnb.cool/example/repo"), {})
        for status in (401, 403, 500):
            with self.subTest(status=status), patch.object(artifacts, "urlopen",
                    side_effect=HTTPError("url", status, "failed", {}, None)):
                with self.assertRaises(HTTPError):
                    artifacts.previous_release("https://cnb.cool/example/repo")

    def test_daily_force_rebuilds_unchanged_inputs_while_manual_build_skips_them(self):
        """每日构建固定执行，普通手动构建仍可跳过无变化输入。

        Daily builds always run, while ordinary manual builds may skip unchanged inputs.
        """
        previous = {"androidHostCommit": "a" * 40, "azurpilotCommit": "b" * 40}
        env = {"CNB_BUILD_ID": "cnb-daily-123", "CNB_REPO_SLUG": "azurpilot/test",
               "CNB_WEB_ENDPOINT": "https://cnb.cool", "AZURPILOT_REF": "b" * 40}
        for force, expected in (("true", "true"), ("false", "false")):
            with self.subTest(force=force), patch.dict(artifacts.os.environ, {**env, "FORCE": force}, clear=True), \
                    patch.object(artifacts, "previous_release", return_value=previous), \
                    patch.object(artifacts, "version_inputs", return_value=(1700000000, "1.3.0", "a" * 40)), \
                    patch.object(artifacts, "changed", return_value=False):
                output = io.StringIO()
                with redirect_stdout(output):
                    artifacts.resolve()
                self.assertIn(f"##[set-output SHOULD_BUILD={expected}]", output.getvalue())

    def test_runtime_only_build_preserves_app_version(self):
        """运行时变化不前进应用版本。 / Runtime-only builds retain the previous app version."""
        for name in ("1.2.100", "1.3.0", "1.3.42"):
            with self.subTest(name=name):
                previous = {"versionCode": 1700000000, "versionName": name, "appCommit": "c" * 40}
                with patch.object(artifacts, "git", side_effect=["a" * 40, "1800000000", "305"]), \
                        patch.object(artifacts, "comparable", return_value=True), \
                        patch.object(artifacts, "changed", return_value=False):
                    self.assertEqual(artifacts.version_inputs(previous),
                                     (1700000000, name, "c" * 40))

    def test_app_build_cannot_downgrade_version_code(self):
        """应用变化时版本号高于上次发布和提交时间。 / Advances beyond the release and commit time."""
        for name in ("1.2.100", "1.3.1"):
            with self.subTest(name=name):
                previous = {"versionCode": 1900000000, "versionName": name, "appCommit": "c" * 40}
                with patch.object(artifacts, "git", side_effect=["a" * 40, "1800000000", "305"]), \
                        patch.object(artifacts, "comparable", return_value=True), \
                        patch.object(artifacts, "changed", return_value=True):
                    self.assertEqual(artifacts.version_inputs(previous),
                                     (1900000001, "1.3.2", "a" * 40))

    def test_version_series_starts_at_the_new_baseline(self):
        """版本基准从 1.3.0 起算，之后每个提交递增。

        Starts the version baseline at 1.3.0 and increments for later commits.
        """
        for count, name in ((302, "1.3.0"), (303, "1.3.0"), (304, "1.3.1")):
            with self.subTest(count=count), \
                    patch.object(artifacts, "git", side_effect=["a" * 40, "1800000000", str(count)]):
                self.assertEqual(artifacts.version_inputs({}), (1800000000, name, "a" * 40))


if __name__ == "__main__":
    unittest.main()

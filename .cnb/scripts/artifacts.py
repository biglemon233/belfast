"""解析 CNB 构建输入并生成可校验的国内下载产物。

Resolves CNB build inputs and prepares verifiable domestic download artifacts.
"""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
from urllib.error import HTTPError
from urllib.parse import quote
from urllib.request import urlopen


ABIS = ("arm64-v8a", "x86_64")
SHA_PATTERN = re.compile(r"[0-9a-f]{40}")
SIGNING_KEYS = (
    "AZURPILOT_ANDROID_KEYSTORE_BASE64",
    "AZURPILOT_ANDROID_KEYSTORE_PASSWORD",
    "AZURPILOT_ANDROID_KEY_ALIAS",
    "AZURPILOT_ANDROID_KEY_PASSWORD",
)
REPORT_KEYS = ("AZURPILOT_DEVICE_REPORT_CERT_BASE64", "AZURPILOT_DEVICE_REPORT_KEY_BASE64")


def git(*args: str) -> str:
    """返回 Git 输出，失败时中止。 / Returns Git output and fails on errors."""
    return subprocess.check_output(["git", *args], text=True).strip()


def comparable(commit: object) -> bool:
    """仅接受当前历史中可比较的提交。 / Accepts only ancestors in the current history."""
    return isinstance(commit, str) and SHA_PATTERN.fullmatch(commit) is not None and (
        subprocess.run(["git", "merge-base", "--is-ancestor", commit, "HEAD"],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
    )


def changed(commit: object, paths: tuple[str, ...]) -> bool:
    """基线缺失时要求重建。 / Requires rebuilding when the baseline is unavailable."""
    if not comparable(commit):
        return True
    result = subprocess.run(["git", "diff", "--quiet", str(commit), "HEAD", "--", *paths])
    if result.returncode not in (0, 1):
        raise RuntimeError("Could not compare artifact inputs")
    return result.returncode == 1


def signing_ready(env: dict[str, str]) -> bool:
    """拒绝不完整的正式签名和上报凭据。 / Rejects partial release and reporting credentials."""
    signing = [bool(env.get(key)) for key in SIGNING_KEYS]
    reporting = [bool(env.get(key)) for key in REPORT_KEYS]
    if any(signing) and not all(signing):
        raise ValueError("Release signing secrets are incomplete; see doc/cnb-ci.md")
    if any(reporting) and not all(reporting):
        raise ValueError("Device reporting certificate and key must be supplied together")
    if all(signing) and not all(reporting):
        raise ValueError("Signed releases require device reporting credentials; see doc/cnb-ci.md")
    return all(signing)


def version_inputs(previous: dict) -> tuple[int, str, str]:
    """与 GitHub CI 保持相同版本规则。 / Uses the same version rules as GitHub CI."""
    commit = git("rev-parse", "HEAD")
    code = int(git("log", "-1", "--format=%ct", "HEAD"))
    name = f"1.3.{max(int(git('rev-list', '--count', 'HEAD')) - 303, 0)}"
    old_code = previous.get("versionCode")
    old_name = previous.get("versionName")
    old_commit = previous.get("appCommit")
    if (isinstance(old_code, int) and not isinstance(old_code, bool) and old_code > 0
            and isinstance(old_name, str) and re.fullmatch(r"1\.[123]\.\d+", old_name)
            and comparable(old_commit)):
        if not changed(old_commit, ("app",)):
            code, name, commit = old_code, old_name, old_commit
        else:
            code = max(code, old_code + 1)
    if not 0 < code <= 2147483647:
        raise ValueError("Invalid Android version code")
    return code, name, commit


def previous_release(base: str) -> dict:
    """读取上次完整发布；只有 404 才视为首次构建。

    Reads the last complete release; only a 404 is treated as the first build.
    """
    try:
        with urlopen(f"{base}/-/releases/latest/download/latest.json", timeout=60) as response:
            result = json.load(response)
    except HTTPError as error:
        if error.code == 404:
            return {}
        raise
    if not isinstance(result, dict):
        raise ValueError("CNB latest.json must contain an object")
    return result


def resolve() -> None:
    """输出非敏感的跨步骤构建输入。 / Exports non-sensitive cross-stage build inputs."""
    env = dict(os.environ)
    ready = signing_ready(env)
    build_key = env["CNB_BUILD_ID"]
    if not re.fullmatch(r"[A-Za-z0-9_-]+", build_key):
        raise ValueError("Invalid CNB build identifier")
    source = env.get("AZURPILOT_REPO") or "https://github.com/wess09/AzurPilot.git"
    ref = env.get("AZURPILOT_REF", "").strip()
    if not ref:
        output = git("ls-remote", source, "refs/heads/dev").split()
        ref = output[0] if output else ""
    if SHA_PATTERN.fullmatch(ref) is None:
        raise ValueError("AZURPILOT_REF must be a full 40-character commit SHA")
    base = f"{env['CNB_WEB_ENDPOINT'].rstrip('/')}/{env['CNB_REPO_SLUG']}"
    previous = previous_release(base)
    code, name, commit = version_inputs(previous)
    inputs_changed = changed(previous.get("androidHostCommit"),
                             ("app", "rootfs", ".cnb.yml", ".cnb"))
    should_build = (env.get("FORCE", "").lower() == "true" or inputs_changed
                    or previous.get("azurpilotCommit") != ref)
    print(f"Upstream: {ref}; inputs changed: {inputs_changed}; build: {should_build}; "
          f"version: {name} ({code}); signing ready: {ready}")
    outputs = {
        "SHOULD_BUILD": str(should_build).lower(), "SIGNING_READY": str(ready).lower(),
        "AZURPILOT_REF": ref, "BUILD_KEY": build_key, "APP_VERSION_CODE": str(code),
        "APP_VERSION_NAME": name, "APP_COMMIT": commit, "RELEASE_TAG": f"cnb-android-{build_key}",
    }
    for key, value in outputs.items():
        print(f"##[set-output {key}={value}]")


def credentials() -> None:
    """私密输入仅落在被忽略的临时目录。 / Writes secrets only to ignored temporary paths."""
    env = dict(os.environ)
    ready = signing_ready(env)
    files = []
    if ready:
        files.append(("AZURPILOT_ANDROID_KEYSTORE_BASE64", Path(".tmp/signing/azurpilot.jks")))
    if env.get(REPORT_KEYS[0]):
        files.extend(zip(REPORT_KEYS, (Path(".tmp/report-credentials/client-cert.pem"),
                                       Path(".tmp/report-credentials/client-key.pem"))))
    for key, path in files:
        data = base64.b64decode(env[key], validate=True)
        if not data:
            raise ValueError(f"{key} decodes to an empty file")
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        path.write_bytes(data)
        path.chmod(0o600)


def verify_rootfs(root: Path, upstream: str, host: str) -> dict[str, dict]:
    """核对两架构的来源，拒绝混入旧构建。 / Rejects stale or mismatched runtime inputs."""
    manifests = {}
    for abi in ABIS:
        directory = root / abi
        manifest = json.loads((directory / "BUILD_MANIFEST").read_text(encoding="utf-8"))
        expected = {"runtime": "azurpilot-android", "rootfs_arch": abi,
                    "azurpilot_commit": upstream, "android_host_commit": host}
        if any(manifest.get(key) != value for key, value in expected.items()):
            raise ValueError(f"Runtime provenance mismatch: {abi}")
        if not manifest.get("rootfs_version") or not (directory / "rootfs.tar.xz").stat().st_size:
            raise ValueError(f"Incomplete runtime: {abi}")
        manifests[abi] = manifest
    return manifests


def digest(path: Path) -> str:
    """流式计算大产物哈希。 / Hashes large artifacts without loading them into memory."""
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def prepare_release(root: Path, dist: Path, env: dict[str, str]) -> dict:
    """生成 APK、运行时、清单及校验和，不发布任何远程资源。

    Prepares APKs, runtimes, manifests, and checksums without publishing remote resources.
    """
    host, upstream = env["CNB_COMMIT"], env["AZURPILOT_REF"]
    manifests = verify_rootfs(root, upstream, host)
    ready = env["SIGNING_READY"] == "true"
    metadata = json.loads((dist / "apk-version.json").read_text(encoding="utf-8"))
    code, name = metadata["versionCode"], metadata["versionName"]
    if code != int(env["APP_VERSION_CODE"]) or name != env["APP_VERSION_NAME"]:
        raise ValueError("APK output version does not match resolved build inputs")
    output = dist / "release"
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError("Release output directory must be empty")
    base = (f"{env['CNB_WEB_ENDPOINT'].rstrip('/')}/{env['CNB_REPO_SLUG']}"
            f"/-/releases/download/{quote(env['RELEASE_TAG'], safe='')}")
    copied = []

    def asset(source: Path, filename: str) -> dict:
        """复制并描述一个下载文件。 / Copies and describes a downloadable file."""
        target = output / filename
        shutil.copyfile(source, target)
        if not target.stat().st_size:
            raise ValueError(f"Empty download artifact: {filename}")
        copied.append(target)
        entry = {"sha256": digest(target), "size": target.stat().st_size}
        if ready:
            entry["url"] = f"{base}/{quote(filename, safe='')}"
        else:
            entry["file"] = filename
        return entry

    suffix = "" if ready else "-debug"
    apk = asset(dist / "azurpilot-update.apk", f"AzurPilot-Android-{name}-update{suffix}.apk")
    runtimes, full_apks = {}, {}
    for abi in ABIS:
        runtimes[abi] = {"version": manifests[abi]["rootfs_version"],
                         **asset(root / abi / "rootfs.tar.xz", f"rootfs-{abi}.tar.xz")}
        full_apks[abi] = asset(dist / f"azurpilot-full-{abi}.apk",
                               f"AzurPilot-Android-{name}-{abi}-full{suffix}.apk")
        asset(root / abi / "BUILD_MANIFEST", f"BUILD_MANIFEST-{abi}.json")
    frontend = f"frontend-{upstream}.tar.xz"
    asset(root / ABIS[0] / frontend, frontend)
    # 热更消费者也使用上游脚本生成的单文件校验和。
    asset(root / ABIS[0] / f"{frontend}.sha256", f"{frontend}.sha256")
    manifest = {
        "versionCode": code, "versionName": name, "appCommit": env["APP_COMMIT"],
        "azurpilotCommit": upstream, "androidHostCommit": host,
        "runtimes": runtimes, "fullApks": full_apks,
    }
    if ready:
        arm64 = runtimes["arm64-v8a"]
        manifest.update(apkUrl=apk["url"], apkSha256=apk["sha256"], apkSize=apk["size"],
                        rootfsVersion=arm64["version"], rootfsUrl=arm64["url"],
                        rootfsSha256=arm64["sha256"], rootfsSize=arm64["size"])
        filename = "latest.json"
    else:
        manifest.update(signing="debug", apk=apk)
        filename = "build-info.json"
    index = output / filename
    index.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    copied.append(index)
    (output / "SHA256SUMS").write_text(
        "".join(f"{digest(path)}  {path.name}\n" for path in sorted(copied)), encoding="utf-8")
    return manifest


def main() -> None:
    """执行流水线指定阶段。 / Executes the requested pipeline phase."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("resolve", "credentials", "verify-rootfs", "release"))
    action = parser.parse_args().action
    if action == "resolve":
        resolve()
    elif action == "credentials":
        credentials()
    elif action == "verify-rootfs":
        verify_rootfs(Path(".tmp/azurpilot-artifact"), os.environ["AZURPILOT_REF"],
                      os.environ["CNB_COMMIT"])
    else:
        prepare_release(Path(".tmp/azurpilot-artifact"), Path("dist"), dict(os.environ))
        Path(".tmp/cnb-release-notes.md").write_text(
            f"完整包按设备架构选择；轻量包用于已有运行环境的设备。\n\n"
            f"Full APKs bundle the matching runtime. The slim APK reuses an installed runtime.\n\n"
            f"- Android: `{os.environ['CNB_COMMIT']}`\n"
            f"- AzurPilot: `{os.environ['AZURPILOT_REF']}`\n"
            f"- SHA-256: `SHA256SUMS`\n", encoding="utf-8")


if __name__ == "__main__":
    main()

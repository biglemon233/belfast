#!/usr/bin/env bash
# 复用 GitHub CI 的三包构建和正式签名校验，产物只写入 dist。
# Builds the same three APKs as GitHub CI and checks release signatures, writing to dist only.
set -euo pipefail
repo_root="$PWD"
trap 'cd "$repo_root"; bash .cnb/scripts/cleanup.sh' EXIT

bash app/scripts/fetch-proot-libs.sh
python3 app/scripts/test_build_device_catalog.py
python3 app/scripts/build_device_catalog.py
python3 app/scripts/fetch_ocr_runtime.py
python3 app/scripts/test_verify_ocr_assets.py
python3 rootfs/tests/test_android_ocr_adapter.py
uv run --no-project --python 3.12 --with numpy==2.2.6 --with onnx==1.20.1 \
    --with onnxruntime==1.26.0 rootfs/tests/test_android_ocr.py
python3 app/scripts/test_ocr_jni.py
python3 app/scripts/test_ocr_mtk_dispatch.py

variant=debug
if [[ "${SIGNING_READY:?Missing signing state}" == true ]]; then
    variant=release
    export KEYSTORE_PATH="$repo_root/.tmp/signing/azurpilot.jks"
    export KEYSTORE_PASSWORD="$AZURPILOT_ANDROID_KEYSTORE_PASSWORD"
    export KEY_ALIAS="$AZURPILOT_ANDROID_KEY_ALIAS"
    export KEY_PASSWORD="$AZURPILOT_ANDROID_KEY_PASSWORD"
else
    unset KEYSTORE_PATH KEYSTORE_PASSWORD KEY_ALIAS KEY_PASSWORD
fi
cd app
chmod +x gradlew
mkdir -p ../dist app/src/main/assets/rootfs
gradle_args=(--console=plain --stacktrace --no-build-cache
    "-Dorg.gradle.jvmargs=-Xmx6g -XX:MaxMetaspaceSize=1g" "-Pkotlin.daemon.jvmargs=-Xmx3g")
build_variant=":app:assemble${variant^}"
for abi in arm64-v8a x86_64; do
    cp "../.tmp/azurpilot-artifact/$abi/rootfs.tar.xz" app/src/main/assets/rootfs/rootfs.tar.xz
    cp "../.tmp/azurpilot-artifact/$abi/BUILD_MANIFEST" app/src/main/assets/rootfs/BUILD_MANIFEST
    ./gradlew "$build_variant" -Pazurpilot.releaseAbi="$abi" "${gradle_args[@]}"
    cp "app/build/outputs/apk/$variant/"*.apk "../dist/azurpilot-full-$abi.apk"
    rm app/src/main/assets/rootfs/rootfs.tar.xz app/src/main/assets/rootfs/BUILD_MANIFEST
done
./gradlew "$build_variant" -Pazurpilot.slimApk=true "${gradle_args[@]}"
cp "app/build/outputs/apk/$variant/"*.apk ../dist/azurpilot-update.apk
python3 scripts/verify_ocr_assets.py ../dist/*.apk
python3 -c 'import zipfile; z=zipfile.ZipFile("../dist/azurpilot-update.apk"); assert "assets/rootfs/rootfs.tar.xz" not in z.namelist()'
credential_args=()
if [[ -s ../.tmp/report-credentials/client-cert.pem ]]; then
    credential_args+=(--credentials-dir ../.tmp/report-credentials)
fi
python3 scripts/verify_device_report_assets.py "${credential_args[@]}" ../dist/*.apk
python3 - "$variant" <<'PY'
import json, pathlib, sys
metadata = json.loads(pathlib.Path(f'app/build/outputs/apk/{sys.argv[1]}/output-metadata.json').read_text())
entry = metadata['elements'][0]
pathlib.Path('../dist/apk-version.json').write_text(json.dumps({
    'versionCode': entry['versionCode'], 'versionName': entry['versionName'],
}) + '\n')
PY
if [[ "$variant" == release ]]; then
    signer="$ANDROID_HOME/build-tools/36.0.0/apksigner"
    for apk in ../dist/*.apk; do
        for entry in 'v1 23 23' 'v2 24 27' 'v3 28 36'; do
            read -r scheme min_sdk max_sdk <<< "$entry"
            result="$("$signer" verify --verbose --min-sdk-version "$min_sdk" --max-sdk-version "$max_sdk" "$apk")"
            grep -Eq "^Verified using ${scheme} scheme .*: true$" <<< "$result" || {
                echo "$apk is missing a valid $scheme signature" >&2
                exit 1
            }
            echo "$apk: $scheme verified for Android API $min_sdk-$max_sdk"
        done
    done
fi

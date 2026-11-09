#!/usr/bin/env bash
# Build the real OpenCV 4.6 imgcodecs library on Ubuntu 24.04 ARM64.
# Host apt packages: build-essential cmake ninja-build pkg-config binutils python3
#   libjpeg-dev libpng-dev libtiff-dev libwebp-dev libopenjp2-7-dev zlib1g-dev
# Supply the official 4.6.0 source tarball and its independently pinned SHA256.
# Work/output must be fresh, separate directories inside the caller's workspace.
# Only imgcodecs is published; core/imgproc are build intermediates. Validate the
# result against the rootfs's existing core/imgproc and codecs before replacing it.
set -euo pipefail
export LC_ALL=C
die() { printf 'build-slim-imgcodecs: %s\n' "$*" >&2; exit 1; }
usage() {
  echo 'Usage: build_slim_imgcodecs.sh --source-archive FILE --expected-sha256 SHA --work DIR --output DIR'
}
archive='' expected='' work='' output=''
while (( $# )); do
  [[ "$1" != --help ]] || { usage; exit 0; }
  (( $# >= 2 )) || { usage >&2; exit 2; }
  case "$1" in
    --source-archive) archive=$2 ;;
    --expected-sha256) expected=${2,,} ;;
    --work) work=$2 ;;
    --output) output=$2 ;;
    *) die "Unknown argument: $1" ;;
  esac
  shift 2
done
[[ -f "$archive" && "$expected" =~ ^[0-9a-f]{64}$ && -n "$work" && -n "$output" ]] || { usage >&2; exit 2; }
[[ "$(uname -m)" == aarch64 ]] || die 'Use a native Ubuntu 24.04 ARM64 runner.'
# shellcheck disable=SC1091
source /etc/os-release
[[ "$ID" == ubuntu && "$VERSION_ID" == 24.04 ]] || die 'Ubuntu 24.04 is required for the rootfs ABI.'
for tool in cmake ninja pkg-config gcc g++ readelf nm strip python3 sha256sum tar realpath; do
  command -v "$tool" >/dev/null || die "Missing host build tool: $tool"
done
[[ "$(gcc -dumpmachine)" == aarch64-linux-gnu ]] || die 'Unexpected compiler target.'
[[ "$(sha256sum "$archive" | cut -d ' ' -f 1)" == "$expected" ]] || die 'Source archive SHA256 mismatch.'
archive=$(realpath "$archive")
work=$(realpath -m "$work")
output=$(realpath -m "$output")
[[ ! -e "$work" && ! -e "$output" ]] || die 'Work/output must not already exist; keep previous evidence.'
[[ "$work/" != "$output/"* && "$output/" != "$work/"* ]] || die 'Work/output must not overlap.'
mkdir -p "$work/source" "$work/tmp" "$work/cache" "$output"
export TMPDIR="$work/tmp" XDG_CACHE_HOME="$work/cache"
tar -xf "$archive" --strip-components=1 -C "$work/source"
version_header="$work/source/modules/core/include/opencv2/core/version.hpp"
for pair in MAJOR:4 MINOR:6 REVISION:0; do
  grep -Eq "^#define[[:space:]]+CV_VERSION_${pair%:*}[[:space:]]+${pair#*:}[[:space:]]*$" "$version_header" || die 'Source is not OpenCV 4.6.0.'
done
cmake -S "$work/source" -B "$work/build" -G Ninja \
  -DCMAKE_BUILD_TYPE=Release -DCMAKE_CXX_STANDARD=11 \
  -DCMAKE_CXX_FLAGS_RELEASE='-O2 -DNDEBUG' -DCMAKE_SKIP_RPATH=ON \
  -DBUILD_SHARED_LIBS=ON -DBUILD_LIST=core,imgproc,imgcodecs \
  -DBUILD_TESTS=OFF -DBUILD_PERF_TESTS=OFF -DBUILD_EXAMPLES=OFF -DBUILD_DOCS=OFF \
  -DBUILD_opencv_apps=OFF -DBUILD_opencv_python2=OFF -DBUILD_opencv_python3=OFF -DBUILD_JAVA=OFF \
  -DWITH_GDAL=OFF -DWITH_GDCM=OFF -DWITH_OPENEXR=OFF -DBUILD_OPENEXR=OFF \
  -DWITH_JPEG=ON -DWITH_PNG=ON -DWITH_TIFF=ON -DWITH_WEBP=ON -DWITH_OPENJPEG=ON \
  -DBUILD_JPEG=OFF -DBUILD_PNG=OFF -DBUILD_TIFF=OFF -DBUILD_WEBP=OFF -DBUILD_OPENJPEG=OFF -DBUILD_ZLIB=OFF \
  -DWITH_JASPER=OFF -DWITH_IMGCODEC_HDR=ON -DWITH_IMGCODEC_SUNRASTER=ON -DWITH_IMGCODEC_PXM=ON -DWITH_IMGCODEC_PFM=ON \
  -DWITH_IPP=OFF -DWITH_ITT=OFF -DWITH_OPENCL=OFF -DWITH_TBB=OFF -DWITH_EIGEN=OFF -DWITH_LAPACK=OFF \
  -DWITH_PROTOBUF=OFF -DWITH_QUIRC=OFF -DWITH_V4L=OFF -DWITH_FFMPEG=OFF -DWITH_GSTREAMER=OFF \
  -DWITH_GTK=OFF -DWITH_QT=OFF -DWITH_VTK=OFF -DWITH_1394=OFF -DWITH_OPENGL=OFF \
  -DOPENCV_DOWNLOAD_PATH="$work/cache" 2>&1 | tee "$work/configure.log"
for feature in JPEG PNG TIFF OPENJPEG; do
  grep -Eq "^#define HAVE_${feature}([[:space:]]+1)?[[:space:]]*$" "$work/build/cvconfig.h" || die "Required codec unavailable: $feature"
done
grep -q -- '-DHAVE_WEBP' "$work/build/build.ninja" || die 'Required codec unavailable: WEBP'
cmake --build "$work/build" --target opencv_imgcodecs --parallel "$(nproc)" 2>&1 | tee "$work/build.log"
library="$output/libopencv_imgcodecs.so.4.6.0"
install -m 0644 "$work/build/lib/libopencv_imgcodecs.so.4.6.0" "$library"
strip --strip-unneeded "$library"
readelf --wide --file-header --dynamic --version-info "$library" > "$output/ELF_REPORT.txt"
nm -D --defined-only --format=posix "$library" > "$output/DYNAMIC_EXPORTS.txt"
nm -D --undefined-only --format=posix "$library" > "$output/DYNAMIC_IMPORTS.txt"
cp "$work/build/CMakeCache.txt" "$output/CMakeCache.txt"
dpkg-query -W -f='${binary:Package}\t${Version}\t${Architecture}\n' \
  gcc g++ libc6 libstdc++6 libjpeg-dev libpng-dev libtiff-dev libwebp-dev libopenjp2-7-dev zlib1g-dev > "$output/BUILD_PACKAGES.txt"
python3 - "$output" "$expected" <<'PY'
import hashlib, json, pathlib, re, sys
out = pathlib.Path(sys.argv[1])
elf = (out / 'ELF_REPORT.txt').read_text()
needed = sorted(re.findall(r'\(NEEDED\).*?\[([^\]]+)\]', elf))
sonames = re.findall(r'\(SONAME\).*?\[([^\]]+)\]', elf)
assert sonames == ['libopencv_imgcodecs.so.406'], sonames
assert re.search(r'Machine:\s+AArch64\s*$', elf, re.M), 'Not an AArch64 ELF'
assert re.search(r'Class:\s+ELF64\s*$', elf, re.M), 'Not an ELF64 library'
assert not re.search(r'\((?:RPATH|RUNPATH)\)', elf), 'Build path leaked into runtime lookup'
assert not any(re.search(r'gdal|gdcm|openexr|ilm|iex|imath', name, re.I) for name in needed), needed
required = {'libopencv_core.so.406', 'libopencv_imgproc.so.406', 'libjpeg.so.8',
            'libpng16.so.16', 'libtiff.so.6', 'libwebp.so.7', 'libopenjp2.so.7'}
assert required <= set(needed), f'Missing shared codec dependencies: {required - set(needed)}'
exports = (out / 'DYNAMIC_EXPORTS.txt').read_text().splitlines()
assert exports, 'No dynamic exports'
library = out / 'libopencv_imgcodecs.so.4.6.0'
report = dict(opencv_version='4.6.0', source_sha256=sys.argv[2], machine='AArch64',
              soname=sonames[0], needed=needed, dynamic_export_count=len(exports),
              library_sha256=hashlib.sha256(library.read_bytes()).hexdigest(),
              library_bytes=library.stat().st_size,
              disabled_backends=['GDAL', 'GDCM', 'OpenEXR'],
              rootfs_abi_validation='required; not performed by the build script')
(out / 'OPENCV_BUILD.json').write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps(report, indent=2))
PY
ln -s libopencv_imgcodecs.so.4.6.0 "$output/libopencv_imgcodecs.so.406"
printf 'Built %s; validate with the unchanged rootfs core/imgproc before use.\n' "$library"

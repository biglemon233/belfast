# AGENTS.md — AzurPilot for Android

## What this repo is

An Android host app (Kotlin / Jetpack Compose) that bundles a PRoot-based Ubuntu
24.04 runtime to run [AzurPilot](https://github.com/wess09/AzurPilot) — a game
automation tool — entirely on-device, without a PC or root access.
License: AGPL-3.0. Package: `com.azurpilot.ghio`.

## Repository layout

| Path | Purpose |
|---|---|
| `app/` | Gradle root for the Android project (AGP 9 + KGP 2.3, Kotlin DSL) |
| `app/app/` | Main `:app` module — all Kotlin/Compose UI, services, and native bridge code |
| `app/build-logic/` | Convention plugins (`azurpilot.android.*`); SDK/JVM baselines live in `AndroidCommon.kt` |
| `app/annotation-api/`, `app/ksp-processor/` | `@PrefSchema` / `@PrefKey` KSP code-gen for DataStore preferences |
| `app/hidden-api/` | Kotlin compile-only stubs for hidden Android framework APIs |
| `app/app/src/main/native/` | C++17 NDK bridge library (capture, input injection, frame buffer) |
| `app/app/src/main/prootLibs/` | PRoot nine-library set shipped as `jniLibs` (arm64 committed; x86_64 fetched by CI script) |
| `rootfs/` | CI build scripts (`build/`), Python overlay shims (`overlays/`), seed configs (`seeds/`), and tests for the Ubuntu rootfs |
| `doc/` | Bilingual (zh-CN + en) Google-style technical documentation |
| `docs/` | Screenshots used in READMEs |
| `tools/` | Developer utilities (e.g. `watch-android-logs.ps1`) |
| `spike/` | Proof-of-concept experiments (outputs not committed) |
| `handoff/` | Session handoff notes; only `latest.md` is tracked |

### Key source packages (`app/app/…/com/azurpilot/ghio/`)

`auth` · `bridge` · `config` · `constant` · `di` · `domain` · `i18n` ·
`keepalive` · `log` · `overlay` · `privileged` · `proot` · `provision` ·
`remote` · `root` · `service` · `settings` · `theme` · `third` · `ui` ·
`update` · `util` · `widget`

## Build & verify

```bash
# Fast compile check (preferred — unit tests have orphaned stubs that won't compile).
# -x skips the bundled-runtime verify task, which fails when rootfs.tar.xz is absent:
cd app && ./gradlew compileDebugKotlin -x verifyBundledAzurPilotRuntime

# Slim debug APK (no rootfs required):
cd app && ./gradlew assembleDebug

# Full release APK (needs rootfs.tar.xz + BUILD_MANIFEST in assets/rootfs/,
# plus keystore/ signing config):
cd app && ./gradlew assembleRelease

# i18n consistency check (values/ zh-CN base vs values-en/):
python app/scripts/check_i18n_strings.py
```

**Do not run `:app:testDebugUnitTest`** — the test source set has orphaned tests
that fail to compile. Use `compileDebugKotlin` to validate Kotlin changes.

## Architecture essentials

The app has two layers connected over `127.0.0.1:25548`:

1. **App shell** (Kotlin/Compose process) — UI, settings, privileged services
   (Shizuku or root via `app_process`), virtual display management, and the
   native bridge for screen capture and input injection.
2. **PRoot runtime** (Ubuntu 24.04, Python 3.14) — upstream AzurPilot with
   WebUI, WebSocket gateway (`/api/v1/ws`), and a thin Android API
   (`/android/*`).

Key subsystems:
- **RootfsProvisioner** (`provision/`) — first-time deploy & upgrade of the runtime
- **ProotHost** (`proot/`) — spawns and supervises the PRoot session; stdin-pipe keepalive
- **AzurPilotGateway** (`proot/`) — single WebSocket subscriber to the runtime
- **Privileged service** (`privileged/`, `remote/`) — virtual display + bridge; runs in a separate process
- **KeepAlive** (`keepalive/`) — four-fold background persistence (silent audio, 1px overlay, CompanionDeviceService, accessibility daemon)
- **AppWidget** (`widget/`) — Jetpack Glance 1.1.1 MD3 widgets

## Coding conventions

### Kotlin / Compose
- **Source directories**: keep Kotlin sources in `src/<sourceSet>/kotlin/`, including framework API stubs; do not add Java sources or use `java/` source directories.
- **DI**: Koin (module files in `di/`). No Hilt/Dagger.
- **Theme**: Material 3 only. Reuse the existing app theme and Material 3 components.
- **DataStore preferences**: annotate with `@PrefSchema`; KSP generates accessors. Keys follow camelToSnakeCase mapping (`useGithubMirror` → `use_github_mirror`). Migrations must use the snake_case raw key strings.
- **Logging**: Timber. No `Log.d/i/w/e` directly.
- **Serialization**: `kotlinx.serialization` (JSON). No Gson/Moshi.
- **Min SDK 28 / Target SDK 36 / Compile SDK 37**; Java 17; JVM target 17.

### i18n
- `values/strings.xml` = zh-CN (source of truth).
- Translations: `values-en/`, `values-ja/`, `values-zh-rTW/`.
- Run `python app/scripts/check_i18n_strings.py` after touching any strings file.
- The script only validates `values-en/` against `values/` (key parity, placeholder match, residual Chinese detection).

### Comments
- All comments follow [`doc/comment-style.md`](doc/comment-style.md): Google style guides
  (Kotlin KDoc / C++ §7 / Python docstrings) plus the repo bilingual rule —
  doc comments carry Chinese first then the English mirror, inline comments are Chinese
  and explain why, not what.

### Documentation
- `doc/` files are bilingual (Chinese section first, English section second), written in Google developer-doc style.
- All Kotlin files carry bilingual KDoc per [`doc/comment-style.md`](doc/comment-style.md).
- User-facing copy must be ≤ ~100 Chinese characters, plain language, no jargon.

### Native code
- C++17, CMake 3.22.1, `c++_shared` STL.
- Bridge library handles virtual display frame buffer, screen capture, and input injection.

## CI

- **`rootfs.yml`** — builds the Ubuntu rootfs per ABI (arm64-v8a, x86_64), assembles APK, and publishes the same verified signed artifacts to GitHub and CNB Releases. Runs on push to `main` and manual dispatch.
- **`check-upstream.yml`** — polls upstream AzurPilot for changes.
- **`publish-cnb.yml`** — manually mirrors an existing signed GitHub release to CNB without rebuilding. CNB native build and schedule entries are disabled. Setup: `doc/cnb-ci.md`.
- x86_64 PRoot libs are fetched by `app/scripts/fetch-proot-libs.sh` (pinned Termux packages), not committed.

## Gotchas

- The rootfs tarball (`rootfs.tar.xz`, 300 MB+) and `BUILD_MANIFEST` are **not** committed; they're built by CI and copied in before release builds.
- PRoot libraries must be named `lib*.so` and placed in `jniLibs`/`prootLibs` — that's the only `execve`-able location at targetSdk 35+.
- The `keystore/` directory is gitignored. Debug builds work without it.
- On Windows dev machines, `bash` in Python `subprocess` invokes WSL, not Git Bash. Use `uv run` for Python scripts.
- `python3` might be a Windows App Installer stub; probe before using.

## Docs to read before sensitive changes

- [`doc/architecture.md`](doc/architecture.md) — full system architecture (bilingual)
- [`doc/runtime-provisioning.md`](doc/runtime-provisioning.md) — rootfs deploy/upgrade lifecycle
- [`doc/release-channel.md`](doc/release-channel.md) — dual update channels (app + runtime)
- [`doc/multi-arch.md`](doc/multi-arch.md) — ARM64 + x86_64 support constraints

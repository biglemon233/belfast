# 动态图标资源 / Animated icon assets

## 中文

下载、解压、校验和热更使用 [useAnimations](https://useanimations.com/) 的现成图标，
由 [Airbnb Lottie Compose 6.7.1](https://github.com/airbnb/lottie-android/tree/v6.7.1/lottie-compose)
在原生 Compose 界面中播放。仅随 APK 附带下面四份 JSON，不访问远程动画服务。

资源固定在提交 `88b845d9a4330b95bc7f456a17f32ce54157b32f`，原文件逐字节保留。
解压图标原为短悬停动画，以 0.2 倍速往返播放；其他图标按原速循环。
颜色通过 Lottie 的描边和填充回调跟随当前主题。后台暂停播放，关闭系统动画时显示首帧。
加载期间使用标准 Material 图标，步骤说明由相邻文案提供。

图标按上游 MIT 许可使用，保留 `Copyright (c) 2018-2026 useAnimations` 及完整许可正文；
Lottie 按 Apache-2.0 使用。两者均登记在应用的开源许可资产及生成脚本中。

## English

Downloads, extraction, verification, and hot updates use existing icons from
[useAnimations](https://useanimations.com/), rendered natively with
[Airbnb Lottie Compose 6.7.1](https://github.com/airbnb/lottie-android/tree/v6.7.1/lottie-compose).
Only the four JSON files below ship with the APK; playback does not contact a remote animation service.

Assets are pinned to commit `88b845d9a4330b95bc7f456a17f32ce54157b32f` and retained byte for byte.
The archive asset was designed for a short hover interaction; it plays back and forth at 0.2 speed.
Other assets loop at their original speed. Lottie stroke and fill callbacks follow the current theme.
Background pages pause playback, and disabled system animations display the first frame.
Standard Material icons cover loading; adjacent text describes the stage.

Icons use the upstream MIT license, retaining `Copyright (c) 2018-2026 useAnimations` and the full
license text. Lottie uses Apache-2.0. Both are registered in the app's license asset and generator.

## 固定资源 / Pinned assets

| 步骤 / Stage | 本地资源 / Local asset | 上游资源 / Upstream asset | SHA-256 |
| --- | --- | --- | --- |
| 下载 / Download | `transfer_download.json` | [`download2.json`](https://github.com/useAnimations/react-useanimations/blob/88b845d9a4330b95bc7f456a17f32ce54157b32f/icons/download2.json) | `89a2845f9332915e87ec288d71972c20872289e08e9c95d3bc2ce44cacaa0b58` |
| 解压 / Extract | `transfer_extract.json` | [`archive.json`](https://github.com/useAnimations/react-useanimations/blob/88b845d9a4330b95bc7f456a17f32ce54157b32f/icons/archive.json) | `81395d87a3063d4797b982e6d049fe6c687bcd5eb7267f7948cf4e29f1f6147d` |
| 校验 / Verify | `transfer_verify.json` | [`fileText.json`](https://github.com/useAnimations/react-useanimations/blob/88b845d9a4330b95bc7f456a17f32ce54157b32f/icons/fileText.json) | `0e6d3617adf7136258197e28724b53fcf61deb9badd142dcc7beba7f18d31958` |
| 更新 / Apply | `transfer_apply.json` | [`refresh2.json`](https://github.com/useAnimations/react-useanimations/blob/88b845d9a4330b95bc7f456a17f32ce54157b32f/icons/refresh2.json) | `2ee0051d6769e0c41ac690a6abad7ea11600f46b6a0234bb1161510043f7b8ae` |

## 真机预览 / Device preview

调试 APK 注册 `com.azurpilot.ghio.debug.TransferIconPreviewActivity`，以 48、24、20 dp 展示全部
图标，使用应用当前主题，不触发真实下载或部署。发行 APK 不包含这个入口。

Debug APKs register `com.azurpilot.ghio.debug.TransferIconPreviewActivity` to display all icons at
48, 24, and 20 dp in the current app theme without initiating downloads or deployment.
Release APKs do not include this entry point.

```shell
adb shell am start -n com.azurpilot.ghio/.debug.TransferIconPreviewActivity
```

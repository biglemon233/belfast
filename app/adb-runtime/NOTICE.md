# Source and changes / 来源与修改

本模块源自 wess09/shizuku-m 的 manager ADB 客户端和原生配对代码：
https://github.com/wess09/shizuku-m/tree/bcb2b62a255575a93462470dc82c35d36a8340d9/manager/src/main

原项目由 RikkaApps/Shizuku 衍生，按 Apache-2.0 分发；许可证见 LICENSE。
2026-10-13 的移植修改包括包名、JNI 注册、Timber 日志、系统 Conscrypt 反射、连接与读取
超时、本机地址限制、帧长度和认证类型校验、静默重连不提交公钥、资源释放，以及独立测试。
RSA 密钥实现继续使用 Android Keystore AES-GCM 加密；删除原实现的私钥诊断输出。
依赖 BoringSSL 20250114（Prefab）及 Bouncy Castle 1.86（MIT）。

This module derives from the manager ADB client and native pairing code of wess09/shizuku-m
at the pinned commit linked above. The original derives from RikkaApps/Shizuku and is
distributed under Apache-2.0; see LICENSE.

The 2026-10-13 port changes package/JNI names, Timber logging, platform Conscrypt reflection,
connection/read timeouts, loopback-only endpoints, frame-length and authentication-type checks,
silent reconnection without public-key submission, resource cleanup, and isolated tests.
RSA keys remain encrypted with Android Keystore AES-GCM; private-key diagnostic output was
removed. Dependencies include BoringSSL 20250114 (Prefab) and Bouncy Castle 1.86 (MIT).

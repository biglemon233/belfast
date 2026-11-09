// libbridge 基于 AImageReader 的采集生命周期声明。
//
// 实现在特权进程中运行；JNI 入口位于 bridge.cpp，对应 Kotlin NativeBridgeLib.kt 的
// 采集方法。
//
// AImageReader-backed capture lifecycle declarations for libbridge.
// The implementation runs in the privileged process; JNI entry points live in bridge.cpp
// and mirror capture methods in Kotlin NativeBridgeLib.kt.

#ifndef BRIDGE_CAPTURE_H
#define BRIDGE_CAPTURE_H

#include "bridge_internal.h"
#include <string>

// 创建指定显示像素尺寸的采集 Surface；初始化失败时返回 null。
//
// 调用方必须将 setup 与 release 串行。返回 Surface 由 AImageReader 持有；释放采集器后不得
// 再将其用于新的显示管线操作。
//
// Creates a capture Surface at the given display-pixel dimensions; returns null on setup failure.
//
// Callers must serialize setup with release. AImageReader owns the returned Surface; do not use it
// for new display-pipeline operations after releasing the capturer.
jobject SetupNativeCapturer(JNIEnv *env, int width, int height);

// 停止 reader 回调并释放其供给的帧缓冲。
// Stops reader callbacks and releases the frame buffers they feed.
void ReleaseNativeCapturer();

// 返回可从 JNI 请求线程读取的采集诊断快照。
// Returns a capture diagnostic snapshot readable from a JNI request thread.
std::string GetCaptureDiagnostics();

#endif // BRIDGE_CAPTURE_H

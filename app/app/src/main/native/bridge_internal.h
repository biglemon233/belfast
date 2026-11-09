// libbridge 私有公共包含项与日志宏。
//
// 日志标签在特权进程内保持稳定，方便 Kotlin 侧的 Ln 日志和 adb logcat 关联；发布构建
// 编译掉 DEBUG 日志，避免每帧热路径产生格式化开销。
//
// Private common includes and logging macros for libbridge.
// The log tag stays stable in the privileged process so Kotlin Ln output and adb logcat can be
// correlated. Release builds compile out DEBUG logging to avoid formatting cost in frame hot paths.

#ifndef BRIDGE_INTERNAL_H
#define BRIDGE_INTERNAL_H

#include "bridge.h"

#include <android/log.h>

#define LOG_TAG "LibBridge"

#ifdef NDEBUG
#define LOGD(...) ((void) 0)
#else
#define LOGD(...) __android_log_print(ANDROID_LOG_DEBUG, LOG_TAG, __VA_ARGS__)
#endif

#define LOGI(...) __android_log_print(ANDROID_LOG_INFO, LOG_TAG, __VA_ARGS__)
#define LOGW(...) __android_log_print(ANDROID_LOG_WARN, LOG_TAG, __VA_ARGS__)
#define LOGE(...) __android_log_print(ANDROID_LOG_ERROR, LOG_TAG, __VA_ARGS__)

#endif // BRIDGE_INTERNAL_H

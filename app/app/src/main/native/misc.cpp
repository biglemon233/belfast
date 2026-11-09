// libbridge 的共享 JNI 异常收口。
//
// 所有 native 上行调用在异常后都必须清除 JNIEnv 的 pending exception；否则后续 JNI 调用
// 不再具有正常语义。该实现运行在特权进程，供 bridge.cpp 与 bridge_input.cpp 使用。
//
// Shared JNI exception containment for libbridge.
// Every native upcall must clear a pending JNIEnv exception after logging it; otherwise
// later JNI calls no longer have normal semantics. This implementation runs in the
// privileged process and is used by bridge.cpp and bridge_input.cpp.

#include "bridge.h"
#include <android/log.h>

#define LOG_TAG "LibBridge"
#define LOGE(...) __android_log_print(ANDROID_LOG_ERROR, LOG_TAG, __VA_ARGS__)

// 记录、描述并清除 JNI pending exception。
//
// native 上行调用后不能将异常遗留在 JNIEnv 中，否则后续 JNI 调用会失去正常语义。返回 true
// 表示调用前存在异常，调用方应将该次协议操作标记为失败。
//
// Logs, describes, and clears a JNI pending exception.
// Native upcalls must not leave an exception on JNIEnv or later JNI calls lose normal semantics.
// True means an exception existed before cleanup, so the caller must mark that protocol operation
// as failed.
bool CheckJNIException(JNIEnv* env, const char* context) {
    if (env->ExceptionCheck()) {
        LOGE("JNI exception in %s", context);
        env->ExceptionDescribe();
        env->ExceptionClear();
        return true;
    }
    return false;
}


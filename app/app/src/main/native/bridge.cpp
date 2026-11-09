// libbridge 的 JNI 注册与 Kotlin 侧采集入口。
//
// 本文件运行在由 app_process 启动的特权进程中，将 NativeBridgeLib.kt 的 external
// 方法连接到采集、帧缓冲、预览和输入子模块；输入事件再经 DriverClass.kt 上行到 Kotlin。
//
// JNI registration and Kotlin-facing capture entry points for libbridge.
// This file runs in the privileged process launched through app_process. It connects
// NativeBridgeLib.kt external methods to capture, frame-buffer, preview, and input
// modules; input events then call back into Kotlin through DriverClass.kt.

#include "bridge_capture.h"
#include "bridge_frame_buffer.h"
#include "bridge_input.h"
#include "bridge_internal.h"
#include "bridge_preview.h"

#include <android/sharedmem_jni.h>

// 返回固定库标记，供 Kotlin 侧确认 JNI 加载成功。
// Returns the fixed library marker used by Kotlin to confirm JNI loading.
static jstring ping(JNIEnv *env, jclass clazz) {
    (void) clazz;
    return env->NewStringUTF("LibBridge");
}

// 启用运行框架 v5.12.3 之后的多点触摸 contact 字段解析。
// Enables parsing the multi-touch contact field introduced by runtime v5.12.3.
static void nativeSetContactSupport(JNIEnv *env, jclass clazz, jboolean supported) {
    (void) env; (void) clazz;
    SetInputContactSupport(supported == JNI_TRUE);
}

// 将最新 BGR 帧复制为 ARGB_8888 Bitmap；尚无可读帧时返回 null。
// Copies the latest BGR frame into an ARGB_8888 Bitmap; returns null when no frame is readable.
static jobject nativeGetFrameBufferBitmap(JNIEnv *env, jclass clazz) {
    (void) clazz;
    return CreateFrameBufferBitmap(env);
}

// 将最新紧凑 BGR888 帧复制到 Java 字节数组。
//
// GetLockedPixels() 持有已发布槽位，直至本函数调用 UnlockPixels()。锁覆盖 Java 数组分配和
// 一次复制，确保 frame.data 在 JNI 调用期间保持有效；复制后立即解锁，避免后续 Java 处理阻塞
// 采集槽复用。
//
// Copies the latest packed BGR888 frame into a Java byte array.
//
// GetLockedPixels() holds the published slot until this function calls UnlockPixels(). The lock spans
// Java-array allocation and one copy, keeping frame.data valid during the JNI calls. It is released
// immediately afterward so later Java processing cannot block reuse of the capture slot.
static jbyteArray nativeGetFrameBufferBytes(JNIEnv *env, jclass clazz) {
    (void) clazz;
    FrameInfo frame = GetLockedPixels();
    if (!frame.data || frame.length == 0) {
        UnlockPixels(frame);
        return nullptr;
    }
    // 锁必须覆盖 JNI 分配和复制，避免 frame.data 在调用期间被采集线程复用。
    jbyteArray bytes = env->NewByteArray(static_cast<jsize>(frame.length));
    if (bytes) {
        env->SetByteArrayRegion(bytes, 0, static_cast<jsize>(frame.length),
                                reinterpret_cast<const jbyte *>(frame.data));
    }
    UnlockPixels(frame);
    return bytes;
}

// 复制到 SharedMemory 的持久可写映射，避免整帧 Java 数组和 socket 像素传输。
// Copies into SharedMemory's persistent writable mapping, avoiding full-frame Java arrays
// and socket pixel transfers.
static jlongArray nativeCopyFrameBuffer(JNIEnv *env, jclass clazz, jobject destination) {
    (void) clazz;
    void *data = env->GetDirectBufferAddress(destination);
    const jlong capacity = env->GetDirectBufferCapacity(destination);
    int64_t metadata[4]{};
    if (capacity <= 0 || !CopyFrameToBuffer(data, static_cast<size_t>(capacity), metadata)) {
        return nullptr;
    }
    const jlong values[] = {metadata[0], metadata[1], metadata[2], metadata[3]};
    jlongArray result = env->NewLongArray(4);
    if (result) env->SetLongArrayRegion(result, 0, 4, values);
    return result;
}

// 返回独立 FD，由 Kotlin 的 ParcelFileDescriptor 负责关闭。
// Returns an independent FD owned and closed by Kotlin's ParcelFileDescriptor.
static jint nativeDupSharedMemoryFd(JNIEnv *env, jclass clazz, jobject memory) {
    (void) clazz;
    return ASharedMemory_dupFromJava(env, memory);
}

// 替换可选预览目标；传入 null 会关闭预览渲染。
// Replaces the optional preview target; passing null disables preview rendering.
static void nativeSetPreviewSurface(JNIEnv *env, jclass clazz, jobject jSurface) {
    (void) clazz;
    SetPreviewSurface(env, jSurface);
}

// 创建宽高以显示像素计的 RGBA 采集 Surface。
//
// AImageReader 初始化失败时会释放部分状态并返回 null。
//
// Creates an RGBA capture Surface whose dimensions are display pixels.
// AImageReader setup failure releases partial state and returns null.
static jobject nativeSetupNativeCapturer(JNIEnv *env, jclass clazz, jint width, jint height) {
    (void) clazz;
    return SetupNativeCapturer(env, width, height);
}

// 释放采集 reader、预览队列与帧缓冲。
// Releases the capture reader, preview queue, and frame buffers.
static void nativeReleaseNativeCapturer(JNIEnv *env, jclass clazz) {
    (void) env;
    (void) clazz;
    ReleaseNativeCapturer();
}

// 返回当前已初始化帧缓冲生命周期内，已分配给成功转换帧的递增序号。
//
// 释放或重新初始化时计数会归零；该值在 read 指针发布前递增，适合比较本轮进度，但不能单独证明
// 当前线程已经可以读取该帧。
//
// Returns the increasing sequence assigned to successfully converted frames in the current initialized
// frame-buffer lifetime.
//
// Release or reinitialization resets the value. It increments before the read pointer is published,
// so it compares progress within this lifetime only and cannot alone prove that the current thread
// can already read that frame.
static jlong nativeGetFrameCount(JNIEnv *env, jclass clazz) {
    (void) env;
    (void) clazz;
    return static_cast<jlong>(GetFrameCount());
}

// 返回供 UI 与错误报告收集使用的紧凑诊断快照。
// Returns a compact diagnostic snapshot for UI and bug-report collection.
static jstring nativeGetCaptureDiagnostics(JNIEnv *env, jclass clazz) {
    (void) clazz;
    return env->NewStringUTF(GetCaptureDiagnostics().c_str());
}

// 将 NativeBridgeLib.kt 的 external 声明映射到 native 实现。
// Maps NativeBridgeLib.kt external declarations to native implementations.
static JNINativeMethod gMethods[] = {
        {"ping",                  "()Ljava/lang/String;",        reinterpret_cast<void *>(ping)},
        {"setContactSupport",     "(Z)V",                         reinterpret_cast<void *>(nativeSetContactSupport)},
        {"setupNativeCapturer",   "(II)Landroid/view/Surface;",  reinterpret_cast<void *>(nativeSetupNativeCapturer)},
        {"releaseNativeCapturer", "()V",                         reinterpret_cast<void *>(nativeReleaseNativeCapturer)},
        {"setPreviewSurface",     "(Ljava/lang/Object;)V",       reinterpret_cast<void *>(nativeSetPreviewSurface)},
        {"getFrameBufferBitmap",  "()Landroid/graphics/Bitmap;", reinterpret_cast<void *>(nativeGetFrameBufferBitmap)},
        {"getFrameBufferBytes",   "()[B",                      reinterpret_cast<void *>(nativeGetFrameBufferBytes)},
        {"copyFrameBuffer",       "(Ljava/nio/ByteBuffer;)[J",    reinterpret_cast<void *>(nativeCopyFrameBuffer)},
        {"dupSharedMemoryFd",     "(Landroid/os/SharedMemory;)I", reinterpret_cast<void *>(nativeDupSharedMemoryFd)},
        {"getFrameCount",         "()J",                         reinterpret_cast<void *>(nativeGetFrameCount)},
        {"getCaptureDiagnostics", "()Ljava/lang/String;",        reinterpret_cast<void *>(nativeGetCaptureDiagnostics)},
};

// 这里保存的是斜杠分隔的 JNI 二进制类名，而非 Kotlin 源文件路径。R8 必须保留这两个类；
// kNativeBridgeClass 的方法名和描述符必须与 gMethods 一致，kDriverClass 的回调方法描述符
// 必须与 InitInputBridge() 的查找一致。
//
// These are slash-separated JNI binary class names, not Kotlin source paths. R8 must retain both
// classes. kNativeBridgeClass method names and descriptors must match gMethods, while kDriverClass
// callback descriptors must match the lookups in InitInputBridge().
static constexpr char kNativeBridgeClass[] = "com/azurpilot/ghio/bridge/NativeBridgeLib";
static constexpr char kDriverClass[] = "com/azurpilot/ghio/bridge/DriverClass";

// 注册 Kotlin native 方法并解析 DriverClass 上行回调目标。
//
// JNI_OnLoad 在特权进程加载 libbridge 时只运行一次。返回 JNI_ERR 会让
// System.loadLibrary 明确失败，避免暴露半初始化的桥接层。
//
// Registers Kotlin native methods and resolves DriverClass upcall targets.
// JNI_OnLoad runs once when the privileged process loads libbridge. Returning JNI_ERR makes
// System.loadLibrary fail deterministically instead of exposing a partially initialized bridge.
JNIEXPORT jint JNICALL JNI_OnLoad(JavaVM *vm, void *reserved) {
    (void) reserved;

    JNIEnv *env = nullptr;
    if (vm->GetEnv(reinterpret_cast<void **>(&env), JNI_VERSION_1_6) != JNI_OK || !env) {
        return JNI_ERR;
    }

    jclass nativeLibClass = env->FindClass(kNativeBridgeClass);
    if (!nativeLibClass) {
        CheckJNIException(env, "FindClass(NativeBridgeLib)");
        return JNI_ERR;
    }

    if (env->RegisterNatives(
            nativeLibClass, gMethods,
            static_cast<jint>(sizeof(gMethods) / sizeof(gMethods[0]))) < 0) {
        CheckJNIException(env, "RegisterNatives(NativeBridgeLib)");
        env->DeleteLocalRef(nativeLibClass);
        return JNI_ERR;
    }
    env->DeleteLocalRef(nativeLibClass);

    if (!InitInputBridge(vm, env, kDriverClass)) {
        return JNI_ERR;
    }

    return JNI_VERSION_1_6;
}

// 在库卸载前释放全局 JNI 引用和 native 资源。
// Drops global JNI references and native resources before the library unloads.
JNIEXPORT void JNICALL JNI_OnUnload(JavaVM *vm, void *reserved) {
    (void) reserved;

    JNIEnv *env = nullptr;
    if (vm->GetEnv(reinterpret_cast<void **>(&env), JNI_VERSION_1_6) == JNI_OK && env) {
        SetPreviewSurface(env, nullptr);
        ReleaseInputBridge(env);
    }
}

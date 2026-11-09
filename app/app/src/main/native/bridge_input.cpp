// 运行框架输入协议到 Kotlin 隐藏 API 注入器的 JNI 上行桥。
//
// 本文件在特权进程的框架/JNA 回调线程中运行。它解析 bridge.h 的 MethodParam，附着
// 当前线程到 JVM 后回调 DriverClass.kt；该类再通过 InputControlUtils.kt 执行注入。
//
// JNI upcall bridge from the runtime input protocol to Kotlin's hidden-API injector.
// This file runs on framework/JNA callback threads in the privileged process. It parses
// bridge.h MethodParam values, attaches the current thread to the JVM, and calls
// DriverClass.kt, which injects through InputControlUtils.kt.

#include <unistd.h>
#include <atomic>
#include <cstring>
#include "bridge_input.h"

// 缓存 JVM、DriverClass 全局引用和静态回调方法。
//
// 它们由 InitInputBridge() 在 JNI_OnLoad 中建立，并由 ReleaseInputBridge() 在卸载时
// 成对清理；框架回调线程只能读取已经完整初始化的缓存。
//
// Cached JVM, DriverClass global reference, and static callback methods.
// InitInputBridge() establishes them from JNI_OnLoad and ReleaseInputBridge() clears them during
// unload. Framework callback threads may read only the fully initialized cache.
static JavaVM *g_jvm = nullptr;
static jclass g_driver_clz = nullptr;
static jmethodID g_touch_down_method = nullptr;
static jmethodID g_touch_move_method = nullptr;
static jmethodID g_touch_up_method = nullptr;
static jmethodID g_key_down_method = nullptr;
static jmethodID g_key_up_method = nullptr;
static jmethodID g_start_app_method = nullptr;

// runtime v5.12.3 才由框架填充 TouchArgs.contact；旧 runtime 不写入它，残留栈值可能
// 落在有效触点范围并变成幻影手指。Kotlin 完成版本判定前必须保持 false，统一按 contact 0。
//
// Runtime v5.12.3 is the first version that fills TouchArgs.contact. Older runtimes leave the
// field unwritten, so stale stack data can look like a valid contact and create a phantom finger.
// This remains false until Kotlin completes version detection, forcing contact 0.
static std::atomic_bool g_read_contact{false};

// 设置当前运行框架是否提供可信的多点触摸 contact 字段。
// Sets whether the current runtime provides a trustworthy multi-touch contact field.
void SetInputContactSupport(bool supported) {
    g_read_contact.store(supported, std::memory_order_relaxed);
}

// 读取经版本门控后的 contact；不支持时安全退化为单触点 0。
// Reads the version-gated contact; safely falls back to single contact 0 when unsupported.
static int TouchContact(const MethodParam &param) {
    return g_read_contact.load(std::memory_order_relaxed) ? param.args.touch.contact : 0;
}

// Kotlin 上行会经隐藏 API 反射，在各 OEM ROM 上抛异常是正常失败模式。JNI pending
// exception 若不清除，会让下一次 JNI 调用失去正常语义；实测会在后续 NewStringUTF 中
// SIGSEGV。因此每次上行结束都必须走这里清理。
//
// Kotlin upcalls use hidden-API reflection, whose OEM-ROM failures commonly throw exceptions. A
// JNI pending exception must be cleared or later JNI calls lose normal semantics; observed cases
// crashed in a later NewStringUTF. Every upcall must therefore finish through this helper.
static int FinishUpcall(JNIEnv *env, jboolean result, const char *context) {
    if (CheckJNIException(env, context)) {
        return -1;
    }
    return result ? 0 : -1;
}

// 将触摸或按键协议消息同步上行到 DriverClass。
//
// x/y 是目标逻辑显示器内的像素坐标，contact 是零起始触点编号，keyCode 是标准 Android
// 键码。DriverClass 返回 false 与 JNI 异常都统一转换为 -1。
//
// Synchronously upcalls a touch or key protocol message to DriverClass.
// x/y are pixel coordinates in the target logical display, contact is a zero-based pointer
// index, and keyCode is a standard Android key code. A false DriverClass result or JNI exception
// is normalized to -1.
static int
UpcallInputControl(JNIEnv *env, MethodType method, int x, int y, int contact, int keyCode,
                   int displayId) {
    if (!env || !g_driver_clz) {
        return -1;
    }

    switch (method) {
        case TOUCH_DOWN:
            return FinishUpcall(env,
                                env->CallStaticBooleanMethod(g_driver_clz, g_touch_down_method, x,
                                                             y, contact, displayId),
                                "DriverClass.touchDown");
        case TOUCH_MOVE:
            return FinishUpcall(env,
                                env->CallStaticBooleanMethod(g_driver_clz, g_touch_move_method, x,
                                                             y, contact, displayId),
                                "DriverClass.touchMove");
        case TOUCH_UP:
            return FinishUpcall(env,
                                env->CallStaticBooleanMethod(g_driver_clz, g_touch_up_method, x, y,
                                                             contact, displayId),
                                "DriverClass.touchUp");
        case KEY_DOWN:
            return FinishUpcall(env,
                                env->CallStaticBooleanMethod(g_driver_clz, g_key_down_method,
                                                             keyCode, displayId),
                                "DriverClass.keyDown");
        case KEY_UP:
            return FinishUpcall(env,
                                env->CallStaticBooleanMethod(g_driver_clz, g_key_up_method, keyCode,
                                                             displayId),
                                "DriverClass.keyUp");
        default:
            return -1;
    }
}

// 将启动请求上行到 DriverClass.startApp。
//
// packageName 来自运行框架的 NUL 结尾字符串。日志限制为 4096 字节的 strnlen，用于定位
// 历史上的越界读取而不无界扫描不可信指针。
//
// Upcalls a launch request to DriverClass.startApp.
// packageName originates as a NUL-terminated runtime string. Logging uses a 4096-byte strnlen to
// diagnose historical out-of-bounds reads without scanning an untrusted pointer without a bound.
static int UpcallStartApp(JNIEnv *env, const char *packageName, int displayId, bool forceStop) {
    if (!env || !packageName || !g_driver_clz || !g_start_app_method) {
        LOGE("UpcallStartApp: not ready env=%p pkg=%p clz=%p mid=%p",
             (void *) env, (void *) packageName, (void *) g_driver_clz, (void *) g_start_app_method);
        return -1;
    }

    // 上游传入 std::string::c_str() 理应带 NUL；限制日志长度可定位历史越界读而不无界扫描。
    LOGI("UpcallStartApp: env=%p len=%zu display=%d forceStop=%d",
         (void *) env, strnlen(packageName, 4096), displayId, (int) forceStop);

    jstring jPackageName = env->NewStringUTF(packageName);
    // 空引用也必须检查并清除异常，不能因短路遗留 JNI pending exception。
    const bool new_string_failed = CheckJNIException(env, "NewStringUTF(packageName)");
    if (!jPackageName || new_string_failed) {
        return -1;
    }
    jboolean result = env->CallStaticBooleanMethod(g_driver_clz, g_start_app_method, jPackageName,
                                                   displayId, static_cast<jboolean>(forceStop));
    int ret = FinishUpcall(env, result, "DriverClass.startApp");
    env->DeleteLocalRef(jPackageName);
    return ret;
}

// 解析 DriverClass 静态方法并保存可跨线程使用的全局类引用。
//
// 必须从 JNI_OnLoad 所在的附着线程调用。任一方法查找失败会删除已建立引用并返回 false，
// 防止半初始化桥接层接收框架回调。
//
// Resolves DriverClass static methods and stores a global class reference usable across threads.
// This must run on the thread attached for JNI_OnLoad. Any failed method lookup removes
// acquired references and returns false so a partially initialized bridge cannot receive framework
// callbacks.
bool InitInputBridge(JavaVM *vm, JNIEnv *env, const char *driverClassName) {
    g_jvm = vm;
    LOGI("InitInputBridge: vm=%p env=%p class=%s", (void *) vm, (void *) env, driverClassName);
    if (!env || !driverClassName) {
        return false;
    }

    jclass driverClass = env->FindClass(driverClassName);
    if (!driverClass || CheckJNIException(env, "FindClass(driverClassName)")) {
        return false;
    }

    g_driver_clz = static_cast<jclass>(env->NewGlobalRef(driverClass));
    env->DeleteLocalRef(driverClass);
    if (!g_driver_clz) {
        return false;
    }

    g_touch_down_method = env->GetStaticMethodID(g_driver_clz, "touchDown", "(IIII)Z");
    g_touch_move_method = env->GetStaticMethodID(g_driver_clz, "touchMove", "(IIII)Z");
    g_touch_up_method = env->GetStaticMethodID(g_driver_clz, "touchUp", "(IIII)Z");
    g_key_down_method = env->GetStaticMethodID(g_driver_clz, "keyDown", "(II)Z");
    g_key_up_method = env->GetStaticMethodID(g_driver_clz, "keyUp", "(II)Z");
    g_start_app_method = env->GetStaticMethodID(g_driver_clz, "startApp", "(Ljava/lang/String;IZ)Z");

    if (CheckJNIException(env, "GetStaticMethodID(DriverClass)") ||
        !g_touch_down_method || !g_touch_move_method || !g_touch_up_method ||
        !g_key_down_method || !g_key_up_method || !g_start_app_method) {
        ReleaseInputBridge(env);
        return false;
    }

    return true;
}

// 删除所有 JNI 回调缓存并恢复保守的单触点模式。
//
// env 可能为空（例如 VM 已处于拆除阶段）；此时仅清空裸方法指针，避免对失效 JVM 进行 JNI
// 调用。
//
// Removes JNI callback caches and restores conservative single-contact mode.
// env may be null while the VM is being torn down; in that case only raw method pointers are
// cleared so no JNI call targets a dying VM.
void ReleaseInputBridge(JNIEnv *env) {
    g_touch_down_method = nullptr;
    g_touch_move_method = nullptr;
    g_touch_up_method = nullptr;
    g_key_down_method = nullptr;
    g_key_up_method = nullptr;
    g_start_app_method = nullptr;

    if (g_driver_clz && env) {
        env->DeleteGlobalRef(g_driver_clz);
    }
    g_driver_clz = nullptr;
    g_jvm = nullptr;
    SetInputContactSupport(false);
}
// 由 JNA 等非 JVM 创建线程触发上行时，在线程退出时解除 daemon 附着。
//
// thread_local 析构可确保每个本地线程最多 detach 一次；JVM 已销毁时 g_jvm 会先在
// ReleaseInputBridge() 中清空。
//
// Detaches daemon-attached JNA and other non-JVM threads when they exit.
// The thread_local destructor ensures each native thread detaches at most once; g_jvm is cleared
// first by ReleaseInputBridge() when the VM is going away.
struct JniThreadDetacher {
    bool armed = false;

    ~JniThreadDetacher() {
        if (armed && g_jvm) {
            g_jvm->DetachCurrentThread();
        }
    }
};


// 取得当前线程的 JNIEnv，必要时以 daemon 身份附着。
//
// 运行框架/JNA 回调线程不由 JVM 创建，故首次上行必须附着；失败返回 null，调用者将该协议
// 消息标记为失败而不会继续解引用 JNI 指针。
//
// Gets JNIEnv for the current thread, attaching it as a daemon when necessary.
// Runtime/JNA callback threads are not JVM-created, so their first upcall must attach. Failure
// returns null so callers fail the protocol message instead of dereferencing a JNI pointer.
static JNIEnv *GetJNIEnv() {
    if (!g_jvm) {
        return nullptr;
    }
    JNIEnv *env = nullptr;
    if (g_jvm->GetEnv(reinterpret_cast<void **>(&env), JNI_VERSION_1_6) == JNI_OK && env) {
        return env;
    }
    if (g_jvm->AttachCurrentThreadAsDaemon(&env, nullptr) != JNI_OK || !env) {
        LOGE("GetJNIEnv: attach failed for thread %d", gettid());
        return nullptr;
    }
    thread_local JniThreadDetacher detacher;
    detacher.armed = true;
    return env;
}

// 将运行框架 MethodParam 分派为 Kotlin 输入或启动上行调用。
//
// 可从任意框架回调线程调用。0 表示成功或当前 ABI 中有意忽略的操作码；-1 表示无法附着 JVM、
// Kotlin 拒绝注入，或 JNI 调用抛出异常。
//
// Dispatches a runtime MethodParam as a Kotlin input or launch upcall.
// This may run on any framework callback thread. Zero means success or an opcode intentionally
// ignored by the current ABI; -1 means JVM attachment failed, Kotlin rejected injection, or a JNI
// call threw an exception.
BRIDGE_API int DispatchInputMessage(MethodParam param) {
    LOGD("DispatchInputMessage: method=%d display_id=%d", param.method, param.display_id);

    auto *env = GetJNIEnv();
    if (!env) {
        return -1;
    }

    switch (param.method) {
        case TOUCH_DOWN:
            return UpcallInputControl(env, TOUCH_DOWN, param.args.touch.p.x, param.args.touch.p.y,
                                      TouchContact(param), 0, param.display_id);
        case TOUCH_MOVE:
            return UpcallInputControl(env, TOUCH_MOVE, param.args.touch.p.x, param.args.touch.p.y,
                                      TouchContact(param), 0, param.display_id);
        case TOUCH_UP:
            return UpcallInputControl(env, TOUCH_UP, param.args.touch.p.x, param.args.touch.p.y,
                                      TouchContact(param), 0, param.display_id);
        case KEY_DOWN:
            return UpcallInputControl(env, KEY_DOWN, 0, 0, 0, param.args.key.key_code,
                                      param.display_id);
        case KEY_UP:
            return UpcallInputControl(env, KEY_UP, 0, 0, 0, param.args.key.key_code,
                                      param.display_id);
        case START_GAME:
            return UpcallStartApp(env, param.args.start_game.package_name, param.display_id,
                                  param.args.start_game.force_stop != 0);
        default:
            return 0;
    }
}

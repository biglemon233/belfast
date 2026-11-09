// libbridge.so 的公共 ABI 头：帧缓冲交接（FrameInfo + Get/UnlockPixels）与
// 输入分发入口（MethodType / MethodParam / DispatchInputMessage）。
// 本库整体运行在特权进程内（由 liblauncher.so 经 app_process 拉起的
// :root_service 进程）：FrameInfo 供同进程内的原生消费者锁定最新帧，
// DispatchInputMessage 把触摸 / 按键 / 启动请求上行给 Kotlin 侧的 DriverClass。
// JNI 静态方法面在 bridge.cpp 注册，对应 Kotlin 侧
// app/app/src/main/kotlin/com/azurpilot/ghio/bridge/（NativeBridgeLib.kt 声明
// external 方法，DriverClass.kt 声明上行回调目标）。
//
// Public ABI header of libbridge.so: the frame-buffer handoff (FrameInfo with
// GetLockedPixels / UnlockPixels) and the input dispatch entry (MethodType /
// MethodParam / DispatchInputMessage).
// The library runs inside the privileged process (the :root_service process
// spawned via app_process by liblauncher.so): FrameInfo lets in-process native
// consumers lock the latest frame, while DispatchInputMessage forwards
// touch / key / start-app requests up to DriverClass on the Kotlin side. The
// JNI static-method surface is registered in bridge.cpp and mirrors the Kotlin
// package app/app/src/main/kotlin/com/azurpilot/ghio/bridge/ (NativeBridgeLib.kt
// declares the external methods; DriverClass.kt declares the upcall targets).

#ifndef NATIVE_LIB_H
#define NATIVE_LIB_H

#include <jni.h>

#include <cstddef>
#include <cstdint>

#ifdef __cplusplus
extern "C" {
#endif

// 导出符号标记：整库按 -fvisibility=hidden 编译（见 CMakeLists.txt），仅此
// C ABI 对进程内消费者显式可见。
//
// Exported-symbol marker: the whole library is built with
// -fvisibility=hidden (see CMakeLists.txt); only this C ABI is explicitly
// visible to in-process consumers.
#define BRIDGE_API __attribute__((visibility("default")))

// 一次锁定持有的帧快照：紧凑 BGR888 排布（无行填充，stride 恒为 width * 3）。
// data 指向共享帧缓冲，frame_ref 是 UnlockPixels 所需的不透明句柄。
//
// A locked frame snapshot: tightly packed BGR888 (no row padding; the stride
// is always width * 3). data points into the shared frame buffer, and
// frame_ref is the opaque handle that must be handed back to UnlockPixels.

struct FrameInfo {
    // 帧的显示像素宽度 / Frame width in display pixels.
    uint32_t width;
    // 帧的显示像素高度 / Frame height in display pixels.
    uint32_t height;
    // 单行 BGR 字节数；恒为 width * 3 / BGR bytes per row; always width * 3.
    uint32_t stride;
    // data 指向区域的总字节数 / Total bytes in the region pointed to by data.
    uint32_t length;
    // 仅在持锁期间有效的 BGR888 数据 / BGR888 data valid only while locked.
    void *data;
    // 必须原样交回 UnlockPixels() 的不透明引用 / Opaque reference returned to UnlockPixels().
    void *frame_ref;
};

// 输入分发协议的操作码。数值是与调用侧约定的取值，只增不改；STOP_GAME /
// INPUT 声明于此仅为 ABI 完整，当前 DispatchInputMessage 不处理（落入
// default 按忽略处理，返回 0）。
//
// Opcodes of the input-dispatch protocol. The numeric values are fixed with
// the calling side and must never be renumbered. STOP_GAME / INPUT stay
// declared for ABI completeness, but DispatchInputMessage does not handle them
// today (they fall through to default and are ignored with a 0 return).
enum MethodType {
    START_GAME = 1,
    STOP_GAME = 2,
    INPUT = 4,
    TOUCH_DOWN = 6,
    TOUCH_MOVE = 7,
    TOUCH_UP = 8,
    KEY_DOWN = 9,
    KEY_UP = 10
};

// 表示触点在逻辑显示器内的像素坐标 / Pointer coordinates in logical-display pixels.
struct Position {
    int x;
    int y;
};

// 各操作码的参数体。package_name 可为 "包名/Activity" 形式；force_stop 非 0
// 表示启动前先强停；STOP_GAME / INPUT 的参数体目前没有分发处理，仅为 ABI
// 完整而保留。
//
// Argument bodies keyed by opcode. package_name may take the
// "package/Activity" form; force_stop non-zero means force-stop before
// starting. The STOP_GAME / INPUT bodies have no dispatch handling today and
// are kept for ABI completeness.

struct StartGameArgs {
    const char *package_name;
    int force_stop;
};

struct StopGameArgs {
    const char *client_type;
};

struct InputArgs {
    const char *text;
};

// 运行框架的触摸参数 ABI。
//
// contact 从运行框架 v5.12.3 起才保证由调用方初始化；旧版本会保留该位置但不写入。因此
// DispatchInputMessage() 仅在 Kotlin 的版本门控已启用时读取它，否则统一使用 contact 0。
//
// Touch-argument ABI from the runtime framework.
//
// Runtime v5.12.3 is the first version guaranteed to initialize contact. Older versions retain the
// field location but leave it unwritten. DispatchInputMessage() reads it only after Kotlin enables
// the version gate; otherwise it always uses contact 0.
struct TouchArgs {
    Position p;
    int contact;
};

struct KeyArgs {
    int key_code;
};

union ArgUnion {
    StartGameArgs start_game;
    StopGameArgs stop_game;
    InputArgs input;
    TouchArgs touch;
    KeyArgs key;
};

// 完整输入请求：display_id 是逻辑显示器 ID，method 选择 args 中的有效联合体成员。
//
// Complete input request: display_id is a logical display ID and method selects
// the valid union member in args.
struct MethodParam {
    int display_id;
    MethodType method;
    ArgUnion args;
};

// 锁定并返回最新发布帧；没有可读帧时 data 为 null。成功返回的值必须交给 UnlockPixels()。
// Locks and returns the newest published frame; data is null when no frame is readable.
// A successful result must be passed to UnlockPixels().
BRIDGE_API FrameInfo GetLockedPixels(void);

// 释放 GetLockedPixels() 取得的读取引用；空 frame_ref 是无操作。
// Releases the read reference acquired by GetLockedPixels(); a null frame_ref is a no-op.
BRIDGE_API int UnlockPixels(FrameInfo info);

// 将框架输入协议分派为 Kotlin 上行调用；0 表示已处理或被有意忽略，-1 表示失败。
// Dispatches a framework input message as a Kotlin upcall; 0 means handled or intentionally
// ignored, while -1 signals failure.
BRIDGE_API int DispatchInputMessage(MethodParam param);

#ifdef __cplusplus
}

// 记录、描述并清除 JNI pending exception；返回值说明调用前是否存在异常。
// Logs, describes, and clears a pending JNI exception; the return value reports whether one
// existed.
bool CheckJNIException(JNIEnv *env, const char *context);

#endif

#endif // NATIVE_LIB_H

// libbridge 输入上行桥的生命周期声明。
//
// 实现将框架线程附着到 JVM 并按名称回调 DriverClass.kt；因此 InitInputBridge() 必须在
// DispatchInputMessage() 之前成功，卸载时由 ReleaseInputBridge() 删除全局引用。
//
// Lifecycle declarations for libbridge's input upcall bridge.
// The implementation attaches framework threads to the JVM and calls DriverClass.kt by name;
// InitInputBridge() must therefore succeed before DispatchInputMessage(), and
// ReleaseInputBridge() removes global references during unload.

#ifndef BRIDGE_INPUT_H
#define BRIDGE_INPUT_H

#include "bridge_internal.h"

// 解析 DriverClass 方法并建立跨 native 线程可用的全局引用。
// Resolves DriverClass methods and establishes global references usable across native threads.
bool InitInputBridge(JavaVM *vm, JNIEnv *env, const char *driverClassName);

// 删除 JNI 回调缓存；在 libbridge 卸载时调用。
// Removes JNI upcall caches; called while libbridge unloads.
void ReleaseInputBridge(JNIEnv *env);

// 设置当前运行框架是否提供可信的 contact 字段。
// Sets whether the current runtime provides a trustworthy contact field.
void SetInputContactSupport(bool supported);

#endif // BRIDGE_INPUT_H

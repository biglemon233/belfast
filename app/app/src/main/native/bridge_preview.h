// libbridge 可选预览 Surface 的异步渲染声明。
//
// 预览永远是尽力而为的旁路：它不能阻塞 AImageReader 采集回调，并可由 Kotlin 传入 null
// 立即关闭。
//
// Asynchronous rendering declarations for libbridge's optional preview Surface.
// Preview is always a best-effort side path: it must not block AImageReader callbacks and
// Kotlin can disable it immediately by passing null.

#ifndef BRIDGE_PREVIEW_H
#define BRIDGE_PREVIEW_H

#include "bridge_internal.h"

#include <media/NdkImage.h>

// 原子替换预览目标；传 null 会停止预览。
// Atomically replaces the preview target; null stops preview.
void SetPreviewSurface(JNIEnv *env, jobject jSurface);

// 返回渲染线程是否可接收预览图像。
// Returns whether the render thread can accept preview images.
bool IsPreviewEnabled();

// 尝试转交 AImage 所有权；true 后由预览器删除 image。
// Attempts to transfer AImage ownership; after true the previewer deletes image.
bool DispatchPreview(AImage *image);

// 删除队列中尚未渲染的图像。
// Deletes images queued but not yet rendered.
void DrainPreviewQueue();

#endif // BRIDGE_PREVIEW_H

// libbridge 三重 BGR 帧缓冲的公开内部契约。
//
// 采集回调发布最新帧；消费者必须将 GetLockedPixels() 与 UnlockPixels() 成对调用，且
// 不得跨越采集器重配置持有 FrameInfo。
//
// Public internal contract for libbridge's triple-buffered BGR frames.
// Capture callbacks publish the newest frame; consumers must pair GetLockedPixels() with
// UnlockPixels() and must not retain FrameInfo across capture reconfiguration.

#ifndef BRIDGE_FRAME_BUFFER_H
#define BRIDGE_FRAME_BUFFER_H

#include "bridge_internal.h"

#include <android/hardware_buffer.h>
#include <media/NdkImage.h>
#include <string>

// 表示一个采集槽是否能被写入；读取引用另由原子计数保护。
// States whether a capture slot can be written; reader references are protected by a separate
// atomic count.
typedef enum {
    FRAME_STATE_FREE = 0,
    FRAME_STATE_WRITING = 2
} FrameBufferState;

// 三个槽允许读取者保留上一帧，同时采集准备下一帧。
// Three slots let a reader retain one frame while capture prepares the next.
#define FRAME_BUFFER_COUNT 3

// 保存一张紧凑 BGR888 帧及其分配与发布元数据。
//
// 写入者在 g_read_buffer 的 release 存储前赋值 frame_count；读取者先 acquire 该指针才读取
// 帧内容。index 在初始化时固定，用于定位同一槽位的原子状态和读取引用计数。
//
// Stores one packed BGR888 frame and its allocation and publication metadata.
//
// Writers assign frame_count before the release store to g_read_buffer; readers acquire that pointer
// before reading frame contents. index is fixed during initialization and identifies the slot's
// atomic state and reader-reference count.
typedef struct {
    uint8_t *bgr_data;
    size_t bgr_size;
    int64_t frame_count;
    int width;
    int height;
    int index;
} FrameBuffer;

// 为指定显示像素尺寸分配帧槽。
// Allocates frame slots for the specified display-pixel dimensions.
void InitFrameBuffers(int width, int height);

// 等待活动读取/写入结束后释放所有槽位。
// Frees every slot after active readers and writers leave.
void ReleaseFrameBuffers();

// 将一张 RGBA AImage 转换并发布为最新 BGR 帧。
// Converts one RGBA AImage and publishes it as the newest BGR frame.
bool WriteImageToFrame(AImage *image);

// 返回最近一次图像平面校验的诊断结果。
// Returns diagnostics from the most recent image-plane validation.
std::string GetFrameReadDiagnostics();

// 将当前 BGR 帧复制为 ARGB_8888 Java Bitmap；无帧时返回 null。
// Copies the current BGR frame into an ARGB_8888 Java Bitmap; returns null when no frame exists.
jobject CreateFrameBufferBitmap(JNIEnv *env);

// 将最新帧复制到调用方的持久映射，返回同一帧的宽、高、长度和序号；失败不写入。
// Copies the latest frame into a caller-owned persistent mapping, returning its width, height,
// length, and sequence. Failure leaves the destination untouched.
bool CopyFrameToBuffer(void *destination, size_t capacity, int64_t metadata[4]);

// 返回当前已初始化帧缓冲生命周期内，已分配给成功转换帧的递增序号。
//
// 释放或重新初始化时计数会归零；该值在 read 指针发布前递增，只能比较本轮进度，不能单独证明
// 调用方已可读取该帧。
//
// Returns the increasing sequence assigned to successfully converted frames in the current initialized
// frame-buffer lifetime.
//
// Release or reinitialization resets the count. The value increments before the read pointer is
// published, so it compares progress within this lifetime only and cannot alone prove that a caller
// can read that frame.
int64_t GetFrameCount();

#endif // BRIDGE_FRAME_BUFFER_H

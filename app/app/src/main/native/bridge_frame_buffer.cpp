// 特权显示采集的三重 BGR 帧缓冲实现。
//
// AImageReader 回调写入最新帧；JNI 与同进程 native 消费者成对使用
// GetLockedPixels()/UnlockPixels() 读取。原子状态避免采集覆盖仍被读取的槽位。
//
// Triple-buffered BGR frame storage for privileged display capture.
// AImageReader callbacks write the newest frame; JNI and in-process native consumers
// read through paired GetLockedPixels()/UnlockPixels() calls. Atomic state prevents
// capture from overwriting a slot that a reader still holds.

#include "bridge_frame_buffer.h"

#include <android/bitmap.h>

#include <atomic>
#include <cstdlib>
#include <cstring>
#include <thread>
#include <mutex>
#include <sstream>

#if defined(__ARM_NEON)

#include <arm_neon.h>

#endif

// 帧槽状态、读取引用和诊断信息。
//
// 状态以原子操作协作采集回调与 JNI 读取者：写入者不使用被读取的槽位，释放时等待所有
// 引用归零后才回收内存。
//
// Frame-slot state, read references, and diagnostics.
// Atomics coordinate capture callbacks with JNI readers: writers never use a slot held by a
// reader, and release waits for all references to reach zero before reclaiming memory.
static FrameBuffer g_buffers[FRAME_BUFFER_COUNT] = {};
static std::atomic<int> g_buffer_states[FRAME_BUFFER_COUNT] = {
        FRAME_STATE_FREE, FRAME_STATE_FREE, FRAME_STATE_FREE
};
static std::atomic<int> g_reader_counts[FRAME_BUFFER_COUNT] = {0, 0, 0};
static std::atomic<FrameBuffer *> g_read_buffer{nullptr};
static std::atomic<int64_t> g_frame_count{0};
static std::atomic<bool> g_frame_buffers_initialized{false};
static std::mutex g_frame_diagnostics_mutex;
static std::string g_frame_diagnostics = "not initialized";

// 返回最近一次 AImage 平面校验的诊断结果。
// Returns the diagnostic result of the most recent AImage-plane validation.
std::string GetFrameReadDiagnostics() {
    std::lock_guard<std::mutex> lock(g_frame_diagnostics_mutex);
    return g_frame_diagnostics;
}

// 将一帧 RGBA/RGB 平面转换为紧凑 BGR888。
//
// NEON 仅加速常见的四字节像素布局；其余格式及尾部像素保持标量路径，以避免将设备相关
// pixel stride 假设扩散到调用者。
//
// Converts one RGBA/RGB image plane to packed BGR888.
// NEON accelerates only the common four-byte pixel layout; other layouts and tail pixels stay
// scalar so device-specific pixel-stride assumptions do not leak to callers.
static void ProcessFrameDataV2(
        const uint8_t *__restrict src,
        uint8_t *__restrict dst_bgr,
        int width,
        int height,
        int src_stride,
        int pixel_stride) {
    for (int y = 0; y < height; ++y) {
        uint8_t *d3 = dst_bgr + y * width * 3;
        int x = 0;

#if defined(__ARM_NEON)
        if (pixel_stride == 4) {
            const uint8_t *s = src + static_cast<size_t>(y) * src_stride;
            for (; x <= width - 16; x += 16) {
                uint8x16x4_t rgba = vld4q_u8(s);
                s += 64;
                uint8x16x3_t bgr;
                bgr.val[0] = rgba.val[2];
                bgr.val[1] = rgba.val[1];
                bgr.val[2] = rgba.val[0];
                vst3q_u8(d3, bgr);
                d3 += 48;
            }
            for (; x <= width - 8; x += 8) {
                uint8x8x4_t rgba = vld4_u8(s);
                s += 32;
                uint8x8x3_t bgr;
                bgr.val[0] = rgba.val[2];
                bgr.val[1] = rgba.val[1];
                bgr.val[2] = rgba.val[0];
                vst3_u8(d3, bgr);
                d3 += 24;
            }
        }
#endif
        for (; x < width; ++x) {
            const uint8_t *pixel = src + static_cast<size_t>(y) * src_stride +
                                   static_cast<size_t>(x) * pixel_stride;
            d3[0] = pixel[2];
            d3[1] = pixel[1];
            d3[2] = pixel[0];
            d3 += 3;
        }
    }
}

static int GetBufferIndex(FrameBuffer *buf) {
    return buf->index;
}

static void ReleaseBuffer(FrameBuffer *buf) {
    if (!buf) {
        return;
    }
    if (buf->bgr_data) {
        free(buf->bgr_data);
        buf->bgr_data = nullptr;
    }
    buf->width = 0;
    buf->height = 0;
    buf->bgr_size = 0;
    buf->frame_count = 0;
}

static void MarkBufferFree(FrameBuffer *buf) {
    int idx = GetBufferIndex(buf);
    if (idx >= 0) {
        g_buffer_states[idx].store(FRAME_STATE_FREE, std::memory_order_release);
    }
}

// 发布已完成写入的槽位。
//
// 先发布 read 指针，再将状态标为可写。否则另一写入者可能在 read 指针切换前取得该槽并覆盖
// 刚完成的帧。read 指针的 release 存储同时发布 BGR 数据、frame_count 和固定 index。
//
// Publishes a completed write slot.
//
// The read pointer is published before marking the slot writable. Otherwise another writer could
// acquire the slot and overwrite the completed frame before the read pointer switches. The read
// pointer's release store also publishes the BGR data, frame_count, and fixed index.
static void CommitWriteBuffer(FrameBuffer *buf) {
    int idx = GetBufferIndex(buf);
    if (idx < 0) {
        return;
    }
    if (g_frame_buffers_initialized.load(std::memory_order_acquire)) {
        g_read_buffer.store(buf, std::memory_order_release);
    }
    g_buffer_states[idx].store(FRAME_STATE_FREE, std::memory_order_release);
}

// 取得一个不等于当前读帧、也未被读取者持有的写槽。
//
// 比较交换后会重新验证条件，因为读取者和重配置线程可能恰好在竞争窗口内更新原子状态。
//
// Acquires a write slot that is neither the current read frame nor held by a reader.
// Conditions are revalidated after compare-exchange because readers and reconfiguration can
// update atomic state in the intervening race window.
static FrameBuffer *AcquireWriteBuffer() {
    if (!g_frame_buffers_initialized.load(std::memory_order_acquire)) {
        return nullptr;
    }

    FrameBuffer *currentReadBuffer = g_read_buffer.load(std::memory_order_acquire);
    for (int i = 0; i < FRAME_BUFFER_COUNT; ++i) {
        FrameBuffer *candidate = &g_buffers[i];
        if (candidate == currentReadBuffer ||
            g_reader_counts[i].load(std::memory_order_acquire) > 0) {
            continue;
        }

        int expected = FRAME_STATE_FREE;
        if (!g_buffer_states[i].compare_exchange_strong(expected, FRAME_STATE_WRITING,
                                                        std::memory_order_acq_rel)) {
            continue;
        }

        if (g_reader_counts[i].load(std::memory_order_acquire) > 0 ||
            g_read_buffer.load(std::memory_order_acquire) == candidate ||
            !g_frame_buffers_initialized.load(std::memory_order_acquire)) {
            g_buffer_states[i].store(FRAME_STATE_FREE, std::memory_order_release);
            continue;
        }
        return candidate;
    }
    return nullptr;
}

// 锁定当前发布帧并为调用者增加读取引用。
//
// 最多重试三次以处理发布者刚切换 read 指针的情况；写入状态短暂存在时只自旋有限次数，
// 防止慢或异常的写入者永久阻塞 JNI 请求线程。
//
// Locks the current published frame and increments its caller-owned read reference.
// It retries at most three times when a publisher just switched the read pointer. A transient
// writing state spins only a bounded number of times so a slow or broken writer cannot block a
// JNI request thread forever.
static const FrameBuffer *LockCurrentFrame() {
    if (!g_frame_buffers_initialized.load(std::memory_order_acquire)) {
        return nullptr;
    }

    for (int attempt = 0; attempt < 3; ++attempt) {
        FrameBuffer *frame = g_read_buffer.load(std::memory_order_acquire);
        if (!frame || frame->frame_count == 0) {
            return nullptr;
        }

        int idx = GetBufferIndex(frame);
        if (idx < 0) {
            return nullptr;
        }

        g_reader_counts[idx].fetch_add(1, std::memory_order_acquire);
        if (g_read_buffer.load(std::memory_order_acquire) != frame ||
            !g_frame_buffers_initialized.load(std::memory_order_acquire)) {
            g_reader_counts[idx].fetch_sub(1, std::memory_order_release);
            continue;
        }

        if (g_buffer_states[idx].load(std::memory_order_acquire) == FRAME_STATE_WRITING) {
            bool ready = false;
            for (int spin = 0; spin < 500; ++spin) {
                if (g_buffer_states[idx].load(std::memory_order_acquire) != FRAME_STATE_WRITING) {
                    ready = true;
                    break;
                }
            }
            if (!ready) {
                g_reader_counts[idx].fetch_sub(1, std::memory_order_release);
                return nullptr;
            }
        }

        return frame;
    }
    return nullptr;
}

static void UnlockFrame(const FrameBuffer *frame) {
    if (!frame) {
        return;
    }

    int idx = GetBufferIndex(const_cast<FrameBuffer *>(frame));
    if (idx >= 0) {
        g_reader_counts[idx].fetch_sub(1, std::memory_order_release);
    }
}

// 仅在一次 memcpy 期间持有帧槽；返回的元数据与像素来自同一个已发布槽。
// Holds a frame slot only for one memcpy; metadata and pixels come from the same published slot.
bool CopyFrameToBuffer(void *destination, size_t capacity, int64_t metadata[4]) {
    const FrameBuffer *frame = LockCurrentFrame();
    if (!frame) return false;
    if (!destination || !frame->bgr_data || frame->bgr_size > capacity) {
        UnlockFrame(frame);
        return false;
    }
    metadata[0] = frame->width;
    metadata[1] = frame->height;
    metadata[2] = static_cast<int64_t>(frame->bgr_size);
    metadata[3] = frame->frame_count;
    memcpy(destination, frame->bgr_data, frame->bgr_size);
    UnlockFrame(frame);
    return true;
}

// 为指定显示像素尺寸分配并重置三重帧缓冲。
//
// 64 字节对齐使标量复制和 NEON 访问保持友好；任一槽分配失败都会回滚此前分配，避免发布
// 半初始化帧集合。
//
// Allocates and resets triple frame buffers for the specified display-pixel dimensions.
// 64-byte alignment keeps scalar copies and NEON access friendly. Failure to allocate any slot
// rolls back earlier allocations so no partially initialized frame set is published.
void InitFrameBuffers(int width, int height) {
    {
        std::lock_guard<std::mutex> lock(g_frame_diagnostics_mutex);
        g_frame_diagnostics = "waiting for image callback";
    }
    if (g_frame_buffers_initialized.load(std::memory_order_acquire)) {
        ReleaseFrameBuffers();
    }

    const size_t bgrSize = static_cast<size_t>(width) * height * 3;
    for (int i = 0; i < FRAME_BUFFER_COUNT; ++i) {
        FrameBuffer &buf = g_buffers[i];
        ReleaseBuffer(&buf);
        if (posix_memalign(reinterpret_cast<void **>(&buf.bgr_data), 64, bgrSize) != 0) {
            LOGE("InitFrameBuffers: posix_memalign failed at index=%d", i);
            for (int j = 0; j <= i; ++j) {
                ReleaseBuffer(&g_buffers[j]);
                g_buffer_states[j].store(FRAME_STATE_FREE, std::memory_order_release);
                g_reader_counts[j].store(0, std::memory_order_release);
            }
            g_read_buffer.store(nullptr, std::memory_order_release);
            g_frame_count.store(0, std::memory_order_release);
            return;
        }

        buf.index = i;
        buf.width = width;
        buf.height = height;
        buf.bgr_size = bgrSize;
        buf.frame_count = 0;
        g_buffer_states[i].store(FRAME_STATE_FREE, std::memory_order_release);
        g_reader_counts[i].store(0, std::memory_order_release);
    }
    g_read_buffer.store(nullptr, std::memory_order_release);
    g_frame_count.store(0, std::memory_order_release);
    g_frame_buffers_initialized.store(true, std::memory_order_release);
    LOGI("InitFrameBuffers: Success %dx%d, waiting for first real frame", width, height);
}

// 停止发布并等待活动读写者离开后释放所有帧缓冲。
//
// 调用方在重新配置或释放 AImageReader 时串行进入；先清除 initialized 可阻止新读取者取得
// 引用，随后才等待已有持有者结束。
//
// Stops publication and frees every frame buffer after active readers and writers leave.
// Callers serialize this with AImageReader reconfiguration or release. Clearing initialized
// first prevents new readers from acquiring references before existing holders are drained.
void ReleaseFrameBuffers() {
    g_frame_buffers_initialized.store(false, std::memory_order_release);
    g_read_buffer.store(nullptr, std::memory_order_release);

    for (int i = 0; i < FRAME_BUFFER_COUNT; ++i) {
        while (g_buffer_states[i].load(std::memory_order_acquire) == FRAME_STATE_WRITING ||
               g_reader_counts[i].load(std::memory_order_acquire) > 0) {
            std::this_thread::yield();
        }

        ReleaseBuffer(&g_buffers[i]);
        g_buffer_states[i].store(FRAME_STATE_FREE, std::memory_order_release);
        g_reader_counts[i].store(0, std::memory_order_release);
    }

    g_read_buffer.store(nullptr, std::memory_order_release);
    g_frame_count.store(0, std::memory_order_release);
}

// 校验 AImage 平面并将其转换、发布为最新 BGR 帧。
//
// 返回 false 表示图像未发布（采集尚未就绪、平面布局不兼容、无可写槽）；错误仅首发一次，
// 防止高频回调淹没 logcat。
//
// Validates an AImage plane, converts it, and publishes it as the newest BGR frame.
// False means the image was not published (capture not ready, incompatible plane layout, or no
// writable slot). Errors log only once so a high-frequency callback cannot flood logcat.
bool WriteImageToFrame(AImage *image) {
    if (!image || !g_frame_buffers_initialized.load(std::memory_order_acquire)) {
        return false;
    }

    int32_t width = 0;
    int32_t height = 0;
    int32_t row_stride = 0;
    int32_t pixel_stride = 0;
    int data_length = 0;
    uint8_t *data = nullptr;
    const media_status_t width_status = AImage_getWidth(image, &width);
    const media_status_t height_status = AImage_getHeight(image, &height);
    const media_status_t row_status = AImage_getPlaneRowStride(image, 0, &row_stride);
    const media_status_t pixel_status = AImage_getPlanePixelStride(image, 0, &pixel_stride);
    const media_status_t data_status = AImage_getPlaneData(image, 0, &data, &data_length);
    int32_t format = 0;
    AImage_getFormat(image, &format);
    {
        std::ostringstream state;
        state << "format=" << format << " size=" << width << "x" << height
              << " row=" << row_stride << " pixel=" << pixel_stride
              << " length=" << data_length << " data=" << (data != nullptr)
              << " statuses(size,row,pixel,data)=" << width_status << "," << height_status
              << "," << row_status << "," << pixel_status << "," << data_status;
        std::lock_guard<std::mutex> lock(g_frame_diagnostics_mutex);
        g_frame_diagnostics = state.str();
    }

    if (width_status != AMEDIA_OK || height_status != AMEDIA_OK ||
        row_status != AMEDIA_OK || pixel_status != AMEDIA_OK ||
        data_status != AMEDIA_OK || !data || pixel_stride < 3) {
        static std::atomic<bool> logged{false};
        if (!logged.exchange(true)) {
            LOGE("AImage plane unavailable: size=%d/%d row=%d pixel=%d data=%d ptr=%p len=%d",
                 width_status, height_status, row_status, pixel_status, data_status,
                 static_cast<void *>(data),
                 data_length);
        }
        return false;
    }

    FrameBuffer *target = AcquireWriteBuffer();
    if (!target) {
        std::lock_guard<std::mutex> lock(g_frame_diagnostics_mutex);
        g_frame_diagnostics += " result=no writable frame buffer";
        return false;
    }
    if (width != target->width || height != target->height ||
        row_stride < width * pixel_stride ||
        data_length < row_stride * (height - 1) + width * pixel_stride) {
        static std::atomic<bool> logged{false};
        if (!logged.exchange(true)) {
            LOGE("AImage plane mismatch: got=%dx%d row=%d pixel=%d len=%d expected=%dx%d",
                 width, height, row_stride, pixel_stride, data_length,
                 target->width, target->height);
        }
        MarkBufferFree(target);
        std::lock_guard<std::mutex> lock(g_frame_diagnostics_mutex);
        g_frame_diagnostics += " result=frame geometry rejected";
        return false;
    }

    ProcessFrameDataV2(data, target->bgr_data, target->width, target->height,
                       row_stride, pixel_stride);

    target->frame_count = g_frame_count.fetch_add(1, std::memory_order_acq_rel) + 1;
    CommitWriteBuffer(target);
    if (target->frame_count == 1) {
        LOGI("Captured first real frame: %dx%d row=%d pixel=%d len=%d",
             width, height, row_stride, pixel_stride, data_length);
    }
    return true;
}

// 返回当前已初始化帧缓冲生命周期内，已分配给成功转换帧的递增序号。
//
// 释放或重新初始化时计数会归零；当前值在 read 指针发布前递增，因此仅可用于比较本轮进度，
// 不能单独推断调用者已经能读取该帧。
//
// Returns the increasing sequence assigned to successfully converted frames in the current initialized
// frame-buffer lifetime.
//
// Release or reinitialization resets the count. The value increments before the read pointer is
// published, so it compares progress within this lifetime only and cannot alone prove that a caller
// can already read that frame.
int64_t GetFrameCount() {
    return g_frame_count.load(std::memory_order_acquire);
}

// 锁定并返回最新帧的 BGR888 快照。
//
// data 为 null 表示没有可读帧。成功结果的 frame_ref 必须且只能传给一次 UnlockPixels()；
// 指针在解锁后立即失效。
//
// Locks and returns a BGR888 snapshot of the newest frame.
// A null data pointer means no frame is readable. The frame_ref of a successful result must be
// passed to UnlockPixels() exactly once; its pointers become invalid immediately after unlock.
BRIDGE_API FrameInfo GetLockedPixels() {
    FrameInfo result = {0};
    const FrameBuffer *frame = LockCurrentFrame();
    if (!frame) {
        return result;
    }

    if (!frame->bgr_data) {
        UnlockFrame(frame);
        return result;
    }

    result.width = frame->width;
    result.height = frame->height;
    result.stride = frame->width * 3;
    result.length = static_cast<uint32_t>(frame->bgr_size);
    result.data = frame->bgr_data;
    result.frame_ref = const_cast<FrameBuffer *>(frame);
    return result;
}

// 释放 GetLockedPixels() 取得的读取引用；空引用是无操作。
// Releases the read reference acquired by GetLockedPixels(); a null reference is a no-op.
BRIDGE_API int UnlockPixels(FrameInfo info) {
    if (info.frame_ref) {
        UnlockFrame(reinterpret_cast<const FrameBuffer *>(info.frame_ref));
    }
    return 0;
}

// 将锁外的 BGR 副本转为 Java ARGB_8888 Bitmap。
//
// JNI 查找或 Bitmap 分配失败会清除 pending exception 并返回 null；本函数绝不持有帧锁，
// 因此 Java 堆压力不会阻塞采集回调。
//
// Converts a BGR copy outside the frame lock into a Java ARGB_8888 Bitmap.
// JNI lookup or Bitmap-allocation failure clears the pending exception and returns null. This
// function never holds a frame lock, so Java heap pressure cannot block capture callbacks.
static jobject BuildArgb8888BitmapFromBgr(JNIEnv *env, const uint8_t *bgr, int width, int height) {
    jclass bitmapClass = env->FindClass("android/graphics/Bitmap");
    jclass configClass = env->FindClass("android/graphics/Bitmap$Config");
    if (!bitmapClass || !configClass) {
        env->ExceptionClear();
        return nullptr;
    }

    jfieldID argb8888Field = env->GetStaticFieldID(
            configClass, "ARGB_8888", "Landroid/graphics/Bitmap$Config;");
    jmethodID createBitmapMethod = env->GetStaticMethodID(
            bitmapClass, "createBitmap",
            "(IILandroid/graphics/Bitmap$Config;)Landroid/graphics/Bitmap;");
    if (!argb8888Field || !createBitmapMethod) {
        env->ExceptionClear();
        return nullptr;
    }

    jobject argb8888Config = env->GetStaticObjectField(configClass, argb8888Field);
    if (!argb8888Config) {
        env->ExceptionClear();
        return nullptr;
    }

    jobject bitmap = env->CallStaticObjectMethod(bitmapClass, createBitmapMethod, width, height,
                                                 argb8888Config);
    if (env->ExceptionCheck()) {
        // createBitmap 可能因 OOM 抛异常，清除后按失败处理
        env->ExceptionClear();
        return nullptr;
    }
    if (!bitmap) {
        return nullptr;
    }

    void *pixels = nullptr;
    if (AndroidBitmap_lockPixels(env, bitmap, &pixels) != ANDROID_BITMAP_RESULT_SUCCESS ||
        !pixels) {
        return nullptr;
    }

    AndroidBitmapInfo info;
    if (AndroidBitmap_getInfo(env, bitmap, &info) != ANDROID_BITMAP_RESULT_SUCCESS) {
        AndroidBitmap_unlockPixels(env, bitmap);
        return nullptr;
    }

    uint8_t *dst = static_cast<uint8_t *>(pixels);
    const uint8_t *src = bgr;
    for (int y = 0; y < height; ++y) {
        for (int x = 0; x < width; ++x) {
            dst[x * 4 + 0] = src[x * 3 + 2];
            dst[x * 4 + 1] = src[x * 3 + 1];
            dst[x * 4 + 2] = src[x * 3 + 0];
            dst[x * 4 + 3] = 255;
        }
        dst += info.stride;
        src += static_cast<size_t>(width) * 3;
    }
    AndroidBitmap_unlockPixels(env, bitmap);
    return bitmap;
}

// 将当前帧复制并转换为 Java Bitmap。
//
// 先复制 BGR 数据再解锁，把持锁时间压缩到一次 memcpy；随后可能较慢的 Bitmap 分配和颜色
// 转换不会与采集或同进程消费者争用帧槽。
//
// Copies and converts the current frame into a Java Bitmap.
// BGR data is copied before unlocking so the frame lock covers only one memcpy. Potentially slow
// Bitmap allocation and color conversion then cannot contend with capture or in-process consumers.
jobject CreateFrameBufferBitmap(JNIEnv *env) {
    FrameInfo frame = GetLockedPixels();
    if (!frame.data || frame.width == 0 || frame.height == 0 || frame.length == 0) {
        UnlockPixels(frame);
        return nullptr;
    }

    // 拷出 BGR 数据后立即解锁，把持锁时间压到一次 memcpy，避免和采集 / 框架 core 抢缓冲区
    auto *bgrCopy = static_cast<uint8_t *>(malloc(frame.length));
    if (!bgrCopy) {
        UnlockPixels(frame);
        return nullptr;
    }
    memcpy(bgrCopy, frame.data, frame.length);
    const int width = static_cast<int>(frame.width);
    const int height = static_cast<int>(frame.height);
    UnlockPixels(frame);

    // 此时已不持有帧锁，bitmap 分配/转换不再阻塞采集
    jobject bitmap = BuildArgb8888BitmapFromBgr(env, bgrCopy, width, height);
    free(bgrCopy);
    return bitmap;
}

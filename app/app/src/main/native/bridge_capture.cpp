// 基于 AImageReader 的特权显示采集端点。
//
// Kotlin 的 NativeBridgeLib.setupNativeCapturer() 取得这里创建的 Surface 并交给系统
// 显示管线；AImageReader 回调线程将最新 RGBA 图像写进共享 BGR 帧缓冲，按需交给预览。
//
// AImageReader-backed capture endpoint for the privileged display bridge.
// Kotlin's NativeBridgeLib.setupNativeCapturer() obtains the Surface created here and
// gives it to the system display pipeline. The AImageReader callback thread writes the
// latest RGBA image to the shared BGR frame buffer and optionally hands it to preview.

#include "bridge_capture.h"

#include "bridge_frame_buffer.h"
#include "bridge_preview.h"

#include <android/native_window.h>
#include <android/native_window_jni.h>
#include <media/NdkImage.h>
#include <media/NdkImageReader.h>
#include <atomic>
#include <sstream>

// 当前采集器的单一所有者。
//
// AImageReader 管理 listener 注册和 getWindow() 返回的借用 ANativeWindow；后者不能单独
// release，只能随 reader 删除。listener.context 保留 NativeCapturer 地址以满足 NDK 回调契约，
// 但当前回调不会解引用它。setup 与 release 由调用方串行。
//
// Single owner of the active capture resources.
//
// AImageReader manages listener registration and the borrowed ANativeWindow returned by getWindow();
// it must not be released independently and instead dies with the reader. listener.context keeps the
// NativeCapturer address for the NDK callback contract, but the current callback does not dereference
// it. Callers serialize setup and release.
struct NativeCapturer {
    AImageReader *reader = nullptr;
    ANativeWindow *window = nullptr;
    AImageReader_ImageListener listener{};
    int width = 0;
    int height = 0;
};

// 仅在串行 setup/release 中替换 g_capturer；g_reader_ready 和其余原子量允许回调线程安全
// 更新诊断状态。
//
// g_capturer is replaced only by serialized setup/release. g_reader_ready and the remaining atomics
// let callback threads update diagnostics safely.
static NativeCapturer *g_capturer = nullptr;
static std::atomic<bool> g_reader_ready{false};
static std::atomic<int64_t> g_callbacks{0}, g_acquired{0}, g_written{0};
static std::atomic<int> g_acquire_status{0}, g_setup_status{0};

// 返回 reader 初始化状态及图像回调、获取和写入的原子计数快照。
// Returns a snapshot of reader setup state and atomic callback, acquisition, and write counts.
std::string GetCaptureDiagnostics() {
    std::ostringstream state;
    state << "reader=" << g_reader_ready.load() << " setupStatus=" << g_setup_status.load()
          << " callbacks=" << g_callbacks.load() << " acquired=" << g_acquired.load()
          << " written=" << g_written.load() << " acquireStatus=" << g_acquire_status.load()
          << " frames=" << GetFrameCount() << " plane={" << GetFrameReadDiagnostics() << "}";
    return state.str();
}

// 在 AImageReader 回调线程消费最新图像。
//
// acquireLatestImage() 刻意丢弃队列中的旧帧：自动化与预览只关心最新状态，保留旧图像会
// 累积端到端延迟。未转交给预览线程的 image 必须在此线程删除。
//
// Consumes the newest image on the AImageReader callback thread.
// acquireLatestImage() deliberately drops queued stale frames: automation and preview only
// need current state, while retaining old images would accumulate end-to-end latency. An image
// not handed to the preview thread must be deleted on this thread.
static void onImageAvailable(void *context, AImageReader *reader) {
    (void) context;
    g_callbacks.fetch_add(1);

    AImage *image = nullptr;
    const auto status = AImageReader_acquireLatestImage(reader, &image);
    g_acquire_status.store(status);
    if (status != AMEDIA_OK || !image) {
        return;
    }

    g_acquired.fetch_add(1);
    if (WriteImageToFrame(image)) g_written.fetch_add(1);

    bool handedOver = false;
    if (IsPreviewEnabled()) {
        handedOver = DispatchPreview(image);
    }

    if (!handedOver) {
        AImage_delete(image);
    }
}

// 替换当前采集器并返回其 producer Surface。
//
// width 和 height 的单位为显示像素。调用方必须与 ReleaseNativeCapturer() 串行；任一步
// AImageReader 配置失败都会清理已取得资源、释放帧缓冲并返回 null。
//
// Replaces the active capturer and returns its producer Surface.
// width and height are display pixels. Callers must serialize this with
// ReleaseNativeCapturer(); any AImageReader setup failure cleans up acquired resources,
// releases frame buffers, and returns null.
jobject SetupNativeCapturer(JNIEnv *env, int width, int height) {
    ReleaseNativeCapturer();
    g_callbacks.store(0);
    g_acquired.store(0);
    g_written.store(0);
    g_acquire_status.store(0);
    g_setup_status.store(0);
    InitFrameBuffers(width, height);

    g_capturer = new NativeCapturer();
    g_capturer->width = width;
    g_capturer->height = height;

    media_status_t status = AImageReader_newWithUsage(
            width, height, AIMAGE_FORMAT_RGBA_8888,
            AHARDWAREBUFFER_USAGE_CPU_READ_OFTEN, 5,
            &g_capturer->reader);
    g_setup_status.store(status);
    if (status != AMEDIA_OK) {
        LOGE("AImageReader_newWithUsage failed: %d", status);
        delete g_capturer;
        g_capturer = nullptr;
        ReleaseFrameBuffers();
        return nullptr;
    }

    g_capturer->listener.context = g_capturer;
    g_capturer->listener.onImageAvailable = onImageAvailable;
    status = AImageReader_setImageListener(g_capturer->reader, &g_capturer->listener);
    g_setup_status.store(status);
    if (status != AMEDIA_OK) {
        LOGE("SetupNativeCapturer: AImageReader_setImageListener failed: %d", status);
        AImageReader_delete(g_capturer->reader);
        delete g_capturer;
        g_capturer = nullptr;
        ReleaseFrameBuffers();
        return nullptr;
    }

    status = AImageReader_getWindow(g_capturer->reader, &g_capturer->window);
    g_setup_status.store(status);
    if (status != AMEDIA_OK || !g_capturer->window) {
        LOGE("SetupNativeCapturer: AImageReader_getWindow failed: status=%d, window=%p",
             status, g_capturer->window);
        AImageReader_setImageListener(g_capturer->reader, nullptr);
        AImageReader_delete(g_capturer->reader);
        delete g_capturer;
        g_capturer = nullptr;
        ReleaseFrameBuffers();
        return nullptr;
    }

    jobject surface = ANativeWindow_toSurface(env, g_capturer->window);
    g_reader_ready.store(surface != nullptr);
    return surface;
}

// 停止帧交付并释放当前采集器拥有的资源。
//
// 删除 AImageReader 前先解除 listener，防止回调在存储拆除过程中交付新图像。
//
// Stops frame delivery and releases resources owned by the active capturer.
// The listener is removed before deleting AImageReader so callbacks cannot deliver new images
// while backing storage is being torn down.
void ReleaseNativeCapturer() {
    g_reader_ready.store(false);
    DrainPreviewQueue();

    if (g_capturer) {
        if (g_capturer->reader) {
            AImageReader_setImageListener(g_capturer->reader, nullptr);
        }
        if (g_capturer->reader) {
            AImageReader_delete(g_capturer->reader);
        }

        delete g_capturer;
        g_capturer = nullptr;
        LOGI("NativeCapturer released");
    }

    ReleaseFrameBuffers();
}

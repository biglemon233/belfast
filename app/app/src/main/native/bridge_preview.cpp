// 可选 Surface 预览的异步渲染器。
//
// 采集回调只移交最新 AImage，独立渲染线程复制到 Kotlin 设置的预览 Surface。队列至多
// 保留一个待渲染帧，并以约 30 FPS 限速，避免预览反压拖慢自动化采集链路。
//
// Asynchronous renderer for the optional Surface preview.
// Capture callbacks hand off only the newest AImage; a dedicated render thread copies it
// to the preview Surface set by Kotlin. The queue keeps at most one pending frame and is
// throttled to roughly 30 FPS so preview backpressure cannot slow the automation capture path.

#include "bridge_preview.h"

#include <android/native_window.h>
#include <android/native_window_jni.h>
#include <media/NdkImage.h>

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstring>
#include <mutex>
#include <queue>
#include <thread>

// 预览 Surface 全局引用、渲染线程和单帧队列。
//
// g_previewMutex 串行 Surface 更换；g_renderMutex 只保护队列及待接管的 ANativeWindow，
// 避免在 Capture 回调热路径中等待 Surface 销毁或 Java 全局引用操作。
//
// Preview Surface global reference, render thread, and one-frame queue.
// g_previewMutex serializes Surface replacement; g_renderMutex protects only the queue and the
// pending ANativeWindow, keeping capture callback hot paths free from Surface teardown and Java
// global-reference work.
static jobject g_previewSurfaceObj = nullptr;
static std::mutex g_previewMutex;
static std::atomic<bool> g_hasPreview{false};
static std::thread g_renderThread;
static std::queue<AImage *> g_renderQueue;
static std::mutex g_renderMutex;
static std::condition_variable g_renderCv;
static std::atomic<bool> g_renderThreadRunning{false};
static ANativeWindow *g_pendingWindow = nullptr;

// 删除队列中仍归预览器所有的 AImage；调用方必须持有 g_renderMutex。
// Deletes AImages still owned by the preview queue; callers must hold g_renderMutex.
static void DrainPreviewQueueLocked() {
    while (!g_renderQueue.empty()) {
        AImage_delete(g_renderQueue.front());
        g_renderQueue.pop();
    }
}

// 将一张 AImage 复制到当前 ANativeWindow。
//
// 本函数只由渲染线程调用。它接受常见的 RGBA 四字节布局，也安全退化为按 pixel stride
// 复制；任何 Surface 锁定或平面读取失败都会丢弃此帧，不能反压采集回调。
//
// Copies one AImage to the active ANativeWindow.
// Only the render thread calls this. It accepts the usual four-byte RGBA layout and safely falls
// back to pixel-stride copying; any Surface-lock or plane-read failure drops this frame rather
// than applying backpressure to the capture callback.
static void RenderPreview(AImage *image, ANativeWindow *window) {
    if (!image || !window) {
        return;
    }

    int32_t width = 0;
    int32_t height = 0;
    int32_t rowStride = 0;
    int32_t pixelStride = 0;
    int dataLength = 0;
    uint8_t *data = nullptr;
    if (AImage_getWidth(image, &width) != AMEDIA_OK ||
        AImage_getHeight(image, &height) != AMEDIA_OK ||
        AImage_getPlaneRowStride(image, 0, &rowStride) != AMEDIA_OK ||
        AImage_getPlanePixelStride(image, 0, &pixelStride) != AMEDIA_OK ||
        AImage_getPlaneData(image, 0, &data, &dataLength) != AMEDIA_OK ||
        !data || pixelStride < 3) {
        return;
    }

    ANativeWindow_setBuffersGeometry(window, width, height, WINDOW_FORMAT_RGBA_8888);
    ANativeWindow_Buffer out{};
    if (ANativeWindow_lock(window, &out, nullptr) != 0 || !out.bits) {
        return;
    }

    auto *dstBase = static_cast<uint8_t *>(out.bits);
    const int dstRowStride = out.stride * 4;
    for (int y = 0; y < height; ++y) {
        const uint8_t *src = data + static_cast<size_t>(y) * rowStride;
        uint8_t *dst = dstBase + static_cast<size_t>(y) * dstRowStride;
        if (pixelStride == 4) {
            memcpy(dst, src, static_cast<size_t>(width) * 4);
        } else {
            for (int x = 0; x < width; ++x) {
                const uint8_t *pixel = src + static_cast<size_t>(x) * pixelStride;
                dst[x * 4] = pixel[0];
                dst[x * 4 + 1] = pixel[1];
                dst[x * 4 + 2] = pixel[2];
                dst[x * 4 + 3] = pixelStride > 3 ? pixel[3] : 255;
            }
        }
    }
    ANativeWindow_unlockAndPost(window);
}

// 在独立线程接管 Surface、渲染最新帧并销毁已消费图像。
//
// 队列为单帧最新值语义：慢预览会跳过旧帧而非无界积压。退出时由该线程释放其持有的
// ANativeWindow，保证 Surface 所有权不跨线程泄漏。
//
// Takes ownership of the Surface, renders the newest frame, and deletes consumed images on a
// dedicated thread. The queue has latest-frame semantics: a slow preview drops old frames rather
// than growing unbounded. This thread releases the ANativeWindow it owns on exit.
static void RenderLoop() {
    ANativeWindow *window = nullptr;
    while (g_renderThreadRunning.load(std::memory_order_acquire)) {
        AImage *image = nullptr;
        {
            std::unique_lock<std::mutex> lock(g_renderMutex);
            g_renderCv.wait(lock, [] {
                return !g_renderThreadRunning.load(std::memory_order_acquire) ||
                       !g_renderQueue.empty() || g_pendingWindow != nullptr;
            });
            if (!g_renderThreadRunning.load(std::memory_order_acquire)) {
                break;
            }
            if (g_pendingWindow) {
                if (window) {
                    ANativeWindow_release(window);
                }
                window = g_pendingWindow;
                g_pendingWindow = nullptr;
            }
            if (!g_renderQueue.empty()) {
                image = g_renderQueue.front();
                g_renderQueue.pop();
            }
        }
        if (image) {
            RenderPreview(image, window);
            AImage_delete(image);
        }
    }
    if (window) {
        ANativeWindow_release(window);
    }
}

// 原子替换 Kotlin 提供的预览 Surface。
//
// 传入 null 会停止并 join 渲染线程、清空队列和删除 Java 全局引用。相同对象无需重启线程；
// 新 Surface 无法转换为 ANativeWindow 时也会恢复为禁用状态。
//
// Atomically replaces the preview Surface supplied by Kotlin.
// Passing null stops and joins the render thread, drains the queue, and deletes the Java global
// reference. The same object does not restart the thread; failure to turn a new Surface into an
// ANativeWindow also restores the disabled state.
void SetPreviewSurface(JNIEnv *env, jobject jSurface) {
    std::lock_guard<std::mutex> lock(g_previewMutex);
    if (g_previewSurfaceObj && env && env->IsSameObject(jSurface, g_previewSurfaceObj)) {
        return;
    }

    if (g_renderThreadRunning.load(std::memory_order_acquire)) {
        g_renderThreadRunning.store(false, std::memory_order_release);
        g_renderCv.notify_all();
        if (g_renderThread.joinable()) {
            g_renderThread.join();
        }
    }
    {
        std::lock_guard<std::mutex> queueLock(g_renderMutex);
        DrainPreviewQueueLocked();
        if (g_pendingWindow) {
            ANativeWindow_release(g_pendingWindow);
            g_pendingWindow = nullptr;
        }
    }
    if (g_previewSurfaceObj && env) {
        env->DeleteGlobalRef(g_previewSurfaceObj);
        g_previewSurfaceObj = nullptr;
    }

    if (jSurface && env) {
        g_previewSurfaceObj = env->NewGlobalRef(jSurface);
        ANativeWindow *window = ANativeWindow_fromSurface(env, jSurface);
        if (window) {
            g_renderThreadRunning.store(true, std::memory_order_release);
            {
                std::lock_guard<std::mutex> queueLock(g_renderMutex);
                g_pendingWindow = window;
            }
            g_renderThread = std::thread(RenderLoop);
        } else {
            env->DeleteGlobalRef(g_previewSurfaceObj);
            g_previewSurfaceObj = nullptr;
        }
    }
    g_hasPreview.store(g_renderThreadRunning.load(std::memory_order_acquire),
                       std::memory_order_release);
}

// 返回预览渲染线程是否可接收图像。
// Returns whether the preview render thread can accept images.
bool IsPreviewEnabled() {
    return g_hasPreview.load(std::memory_order_acquire);
}

// 尝试将图像所有权转交给预览队列。
//
// 为了不影响自动化采集，派发间隔不得小于 33 ms，队列中旧帧会被删除。返回 true 后调用方
// 不得再删除 image；返回 false 时所有权仍属于调用方。
//
// Attempts to transfer image ownership to the preview queue.
// To protect automation capture, dispatches are at least 33 ms apart and an old queued frame is
// deleted. After true, the caller must not delete image; after false, ownership stays with caller.
bool DispatchPreview(AImage *image) {
    if (!image || !g_renderThreadRunning.load(std::memory_order_acquire)) {
        return false;
    }

    static auto lastDispatchTime = std::chrono::steady_clock::now();
    const auto now = std::chrono::steady_clock::now();
    if (std::chrono::duration_cast<std::chrono::milliseconds>(now - lastDispatchTime).count() <
        33) {
        return false;
    }
    lastDispatchTime = now;

    AImage *imageToDelete = nullptr;
    {
        std::lock_guard<std::mutex> lock(g_renderMutex);
        if (!g_renderThreadRunning.load(std::memory_order_acquire)) {
            return false;
        }
        if (!g_renderQueue.empty()) {
            imageToDelete = g_renderQueue.front();
            g_renderQueue.pop();
        }
        g_renderQueue.push(image);
    }
    if (imageToDelete) {
        AImage_delete(imageToDelete);
    }
    g_renderCv.notify_one();
    return true;
}

// 清空尚未由渲染线程消费的预览图像。
// Drains preview images not yet consumed by the render thread.
void DrainPreviewQueue() {
    std::lock_guard<std::mutex> lock(g_renderMutex);
    DrainPreviewQueueLocked();
}

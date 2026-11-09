package com.azurpilot.ghio;

/**
 * 特权输入注入器向 app 侧报告预览手势的单向回调。
 *
 * 回调从特权进程的输入注入路径发出，因此绝不能阻塞。`x`、`y` 是目标显示器中的像素坐标，
 * `type` 是 `MotionEvent.getActionMasked()` 的值。Binder 断开、队列压力或 app 重启时，
 * 调用方必须容忍事件延迟或缺失，不能把本接口当作输入注入成功的确认。
 *
 * One-way callback through which the privileged input injector reports preview gestures to the app.
 *
 * The callback originates on the privileged process's input-injection path and must never block.
 * `x` and `y` are pixel coordinates in the target display, and `type` is the value of
 * `MotionEvent.getActionMasked()`. Callers must tolerate delayed or missing events across Binder
 * disconnects, queue pressure, or app restarts; this interface is not confirmation of successful
 * input injection.
 */
oneway interface ITouchEventCallback {
    /**
     * 报告一个已尝试注入的触摸动作。
     *
     * Reports one touch action whose injection was attempted.
     */
    void onCallback(int x, int y, int type);
}

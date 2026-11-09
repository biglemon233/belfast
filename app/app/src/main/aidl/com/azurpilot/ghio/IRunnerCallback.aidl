package com.azurpilot.ghio;

/**
 * 为 Runtime 执行事件预留的 app 侧单向回调 ABI。
 *
 * 每个方法都使用固定 transaction ID，以便 app 升级时仍可与短暂存活的旧特权进程兼容。
 * 所有回调都是单向调用，生产者不会等待处理结果；消费者必须容忍断线、重连和运行结束附近的
 * 延迟或缺失事件。当前特权服务保留该 ABI，但运行控制实际由 Runtime 链路负责。
 *
 * Reserved app-side one-way callback ABI for Runtime execution events.
 *
 * Every method uses a fixed transaction ID so an upgraded app remains compatible with a briefly
 * surviving old privileged process. All callbacks are one-way: producers do not wait for handling,
 * and consumers must tolerate delayed or missing events around disconnects, reconnects, and run
 * completion. The current privileged service preserves this ABI while the Runtime chain owns actual
 * execution control.
 */
oneway interface IRunnerCallback {

    /**
     * 转发 Runtime 的原始事件及其 JSON 详情。
     *
     * `detailsJson` 的字段由 Runtime 协议定义，消费者必须对未知字段保持兼容。
     *
     * Forwards a raw Runtime event with JSON details.
     *
     * The Runtime protocol defines `detailsJson` fields; consumers must remain compatible with
     * unknown fields.
     */
    void onEvent(String message, String detailsJson) = 1;

    /**
     * 报告某个任务开始；`index` 与 `total` 用于显示当前轮次的进度。
     *
     * Reports task start; `index` and `total` describe progress within the current run.
     */
    void onTaskStarted(String taskName, int index, int total) = 2;

    /**
     * 报告某个任务结束及其结果。
     *
     * Reports task completion and its result.
     */
    void onTaskFinished(String taskName, boolean success, String message) = 3;

    /**
     * 报告整轮运行结束；`outcome` 使用 Runtime 定义的 RunOutcome 值，`reason` 仅在失败时有意义。
     *
     * Reports whole-run completion. `outcome` uses the Runtime-defined RunOutcome value, and
     * `reason` is meaningful only for a failed run.
     */
    void onFinished(int outcome, String reason) = 4;

    /**
     * 转发 agent 标准输出或标准错误的一行。
     *
     * `fromStderr` 为 true 时该行来自标准错误；文本应按日志处理，不能视为结构化协议。
     *
     * Forwards one agent stdout or stderr line.
     *
     * When `fromStderr` is true the line came from stderr. Treat the text as logging, not a
     * structured protocol.
     */
    void onAgentOutput(String line, boolean fromStderr) = 5;

    /**
     * 报告一个 agent child 已连接；可能包含本轮复用的既有连接。
     *
     * `exec` 是 Runtime 的 child-exec 标识，供诊断显示而不是安全授权。
     *
     * Reports that an agent child connected, including a connection reused within the current run.
     *
     * `exec` is the Runtime child-exec identifier for diagnostic display, not an authorization
     * token.
     */
    void onAgentConnected(int index, int total, String exec) = 6;
}

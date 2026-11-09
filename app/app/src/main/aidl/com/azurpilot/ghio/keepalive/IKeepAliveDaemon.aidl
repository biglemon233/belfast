package com.azurpilot.ghio.keepalive;

/**
 * 主进程与 `:daemon` 进程互相绑定时使用的最小存活观测 Binder。
 *
 * 两侧通过 `linkToDeath` 监听此 Binder 的死亡，并在连接丢失后尝试重拉对方服务。该接口不承载
 * 业务状态或可靠心跳；当前 `ping()` 仅是无副作用的可调用探针，Binder 存活不等于保活子系统或
 * Runtime 均健康。
 *
 * Minimal liveness-observation Binder used when the main process and the `:daemon` process bind
 * to each other.
 *
 * Both sides observe this Binder through `linkToDeath` and attempt to restart the peer service
 * after a lost connection. The interface carries no business state or reliable heartbeat. Its
 * current `ping()` is a side-effect-free callable probe; a live Binder does not mean every
 * keep-alive subsystem or the Runtime is healthy.
 */
interface IKeepAliveDaemon {
    /**
     * 执行无副作用的可调用性探针。
     *
     * Performs a side-effect-free callability probe.
     */
    void ping();
}

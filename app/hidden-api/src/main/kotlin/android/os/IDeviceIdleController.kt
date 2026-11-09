package android.os

/**
 * IDeviceIdleController 的隐藏 API 编译期桩，覆盖 Doze 省电白名单的管理。
 * 仅在编译期镜像框架签名，运行时由系统框架提供实现。
 *
 * Compile-time stub of the hidden API `IDeviceIdleController`, covering management of
 * the Doze power-save whitelist. Mirrors the framework signature at compile time only; the
 * real implementation is provided by the platform at runtime.
 */
interface IDeviceIdleController : IInterface {

    /**
     * 把应用加入省电（Doze）白名单，使其在设备空闲时不被系统限制。
     *
     * Adds a package to the power-save (Doze) whitelist so the system does not restrict it
     * while the device is idle.
     *
     * @param packageName 要加白的应用包名 / the package to whitelist
     * @throws RemoteException binder 调用失败时抛出 / if the binder call fails
     */
    @Throws(RemoteException::class)
    fun addPowerSaveWhitelistApp(packageName: String?)

    /**
     * 把应用移出省电（Doze）白名单。
     *
     * Removes a package from the power-save (Doze) whitelist.
     *
     * @param packageName 要移出的应用包名 / the package to remove
     * @throws RemoteException binder 调用失败时抛出 / if the binder call fails
     */
    @Throws(RemoteException::class)
    fun removePowerSaveWhitelistApp(packageName: String?)

    /**
     * 查询应用是否在省电（Doze）白名单中。
     *
     * Queries whether a package is on the power-save (Doze) whitelist.
     *
     * @param packageName 要查询的应用包名 / the package to query
     * @return 在白名单中返回 `true` / `true` if whitelisted
     * @throws RemoteException binder 调用失败时抛出 / if the binder call fails
     */
    @Throws(RemoteException::class)
    fun isPowerSaveWhitelistApp(packageName: String?): Boolean

    /**
     * 保留框架 Stub 的静态入口；编译期签名镜像的函数体不会执行。
     * 使用 object 与 @JvmStatic，使 Kotlin 调用直接链接系统静态方法。
     *
     * Mirrors the framework Stub static entry point; compile-time bodies never execute.
     * An object with @JvmStatic makes Kotlin calls link directly to the platform static method.
     */
    object Stub {
        /**
         * 桩实现，永远抛出；运行时使用框架提供的真正 asInterface。
         *
         * Stub body; always throws. The framework-provided asInterface is used at runtime.
         *
         * @param obj 远端 binder 对象 / the remote binder object
         * @return 永不返回 / never returns
         */
        @JvmStatic
        fun asInterface(obj: IBinder?): IDeviceIdleController {
            throw UnsupportedOperationException()
        }
    }
}

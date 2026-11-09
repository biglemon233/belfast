package android.content

import android.os.Bundle
import android.os.IBinder
import android.os.IInterface
import android.os.RemoteException

/**
 * IContentProvider 的隐藏 API 编译期桩。仅在编译期镜像框架签名，运行时由系统框架提供实现。
 *
 * Compile-time stub of the hidden API `IContentProvider`. Mirrors the framework
 * signature at compile time only; the real implementation is provided by the platform at
 * runtime.
 */
interface IContentProvider : IInterface {

    /**
     * 调用内容提供者方法的最初签名。
     *
     * Calls a content provider method; the original signature.
     *
     * @param callingPkg 调用方包名 / the calling package
     * @param method 要调用的方法名 / the provider-defined method to call
     * @param arg 方法的字符串参数，可为 `null` / the string method argument, may be
     *            `null`
     * @param extras 方法的附加数据，可为 `null` / extra arguments, may be `null`
     * @return 提供者自定义的结果 / the provider-defined result
     * @throws RemoteException binder 调用失败时抛出 / if the binder call fails
     */
    @Throws(RemoteException::class)
    fun call(callingPkg: String?, method: String?, arg: String?, extras: Bundle?): Bundle?

    /**
     * 在初版签名之上增加 authority，用于区分同一调用方可访问的多个提供者。
     *
     * Calls a content provider method with an added authority to disambiguate between the
     * multiple providers reachable from one caller.
     *
     * @param callingPkg 调用方包名 / the calling package
     * @param authority 目标内容提供者的 authority / the authority of the target provider
     * @param method 要调用的方法名 / the provider-defined method to call
     * @param arg 方法的字符串参数，可为 `null` / the string method argument, may be
     *            `null`
     * @param extras 方法的附加数据，可为 `null` / extra arguments, may be `null`
     * @return 提供者自定义的结果 / the provider-defined result
     * @throws RemoteException binder 调用失败时抛出 / if the binder call fails
     */
    @Throws(RemoteException::class)
    fun call(
        callingPkg: String?,
        authority: String?,
        method: String?,
        arg: String?,
        extras: Bundle?,
    ): Bundle?

    /**
     * 在 authority 签名之上再增加调用方的 attribution 标签，用于归属归因。
     *
     * Calls a content provider method, extending the authority signature with the caller's
     * attribution tag for attribution purposes.
     *
     * @param callingPkg 调用方包名 / the calling package
     * @param attributionTag 调用方的归属标签，可为 `null` / the caller's attribution tag,
     *                       may be `null`
     * @param authority 目标内容提供者的 authority / the authority of the target provider
     * @param method 要调用的方法名 / the provider-defined method to call
     * @param arg 方法的字符串参数，可为 `null` / the string method argument, may be
     *            `null`
     * @param extras 方法的附加数据，可为 `null` / extra arguments, may be `null`
     * @return 提供者自定义的结果 / the provider-defined result
     * @throws RemoteException binder 调用失败时抛出 / if the binder call fails
     */
    @Throws(RemoteException::class)
    fun call(
        callingPkg: String?,
        attributionTag: String?,
        authority: String?,
        method: String?,
        arg: String?,
        extras: Bundle?,
    ): Bundle?

    /**
     * 现代签名：直接携带完整的 AttributionSource，替代零散的包名与归属标签。
     *
     * Calls a content provider method carrying a full AttributionSource, superseding the
     * scattered package name and attribution tag.
     *
     * @param attributionSource 调用方的完整归属来源 / the caller's full attribution source
     * @param authority 目标内容提供者的 authority / the authority of the target provider
     * @param method 要调用的方法名 / the provider-defined method to call
     * @param arg 方法的字符串参数，可为 `null` / the string method argument, may be
     *            `null`
     * @param extras 方法的附加数据，可为 `null` / extra arguments, may be `null`
     * @return 提供者自定义的结果 / the provider-defined result
     * @throws RemoteException binder 调用失败时抛出 / if the binder call fails
     */
    @Throws(RemoteException::class)
    fun call(
        attributionSource: AttributionSource?,
        authority: String?,
        method: String?,
        arg: String?,
        extras: Bundle?,
    ): Bundle?

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
        fun asInterface(obj: IBinder?): IContentProvider {
            throw UnsupportedOperationException()
        }
    }
}

package com.aliothmoon.preferences

/**
 * 标记一个偏好 schema 类：类内所有 @PrefKey 属性会生成一个 `${Schema}Schema` object，
 * 含键常量、Defaults 默认值、toXxx 读取与 update 写回
 *
 * Marks a preference schema class: every @PrefKey property inside generates a
 * `${Schema}Schema` object holding key constants, Defaults, a toXxx reader and an update
 * writer.
 */
@Target(AnnotationTarget.CLASS)
@Retention(AnnotationRetention.SOURCE)
annotation class PrefSchema(
    /**
     * 生成 object 的名称前缀，默认使用类名
     * / Name prefix for the generated object; defaults to the class name.
     */
    val name: String = ""
)

package com.aliothmoon.preferences.processor

import com.google.devtools.ksp.processing.CodeGenerator as KspCodeGenerator
import com.google.devtools.ksp.processing.Dependencies
import com.google.devtools.ksp.processing.KSPLogger
import com.google.devtools.ksp.symbol.KSFile
import com.google.devtools.ksp.symbol.KSPropertyDeclaration
import com.google.devtools.ksp.symbol.KSType
import com.squareup.kotlinpoet.*
import com.squareup.kotlinpoet.ParameterizedTypeName.Companion.parameterizedBy
import com.squareup.kotlinpoet.ksp.writeTo

/**
 * 为一个 @PrefSchema 类生成 `<Schema>Schema` object：Preferences 键常量、Defaults 默认值、
 * Preferences→数据类的 toXxx 读取器，以及 DataStore 上的 flow 与 update 写回
 *
 * Generates the `<Schema>Schema` object for one @PrefSchema class: Preferences key constants,
 * Defaults, a Preferences→data-class toXxx reader, plus a flow and an update writer on
 * DataStore.
 */
class SchemaCodeGenerator(
    private val codeGenerator: KspCodeGenerator,
    private val packageName: String,
    private val className: String,
    schemaName: String,
    private val properties: List<KSPropertyDeclaration>,
    private val logger: KSPLogger,
    private val originatingFile: KSFile
) {
    private val schemaObjectName = "${schemaName}Schema"
    private val dataStoreClass = ClassName("androidx.datastore.core", "DataStore")
    private val preferencesClass = ClassName("androidx.datastore.preferences.core", "Preferences")
    private val flowClass = ClassName("kotlinx.coroutines.flow", "Flow")
    private val ioExceptionClass = ClassName("java.io", "IOException")

    /**
     * 生成并写出 schema 文件；声明 originating file，KSP 增量构建才能正确失效缓存
     *
     * Builds and writes the schema file; the originating file is declared so KSP incremental
     * builds invalidate correctly.
     */
    fun generate() {
        logger.info("start process ...")
        val fileSpec = FileSpec.builder(packageName, "${className}Schema")
            .addImport(
                "androidx.datastore.preferences.core", "booleanPreferencesKey", "intPreferencesKey",
                "longPreferencesKey", "floatPreferencesKey", "doublePreferencesKey",
                "stringPreferencesKey", "stringSetPreferencesKey", "edit", "emptyPreferences"
            )
            .addImport("kotlinx.coroutines.flow", "catch", "map")
            .addType(buildSchemaObject())
            .build()
        fileSpec.writeTo(codeGenerator, Dependencies(false, originatingFile))
    }

    private fun buildSchemaObject(): TypeSpec {
        val builder = TypeSpec.objectBuilder(schemaObjectName)

        properties.forEach { prop ->
            val propName = prop.simpleName.asString()
            val keyName = getKeyName(prop)
            val keyType = getPreferencesKeyType(prop)

            builder.addProperty(
                PropertySpec.builder(propName, keyType)
                    .initializer("${getKeyFunctionName(prop)}(%S)", keyName)
                    .build()
            )
        }

        // Defaults 的 Set 成员不能 const，只能 val
        val defaultsBuilder = TypeSpec.objectBuilder("Defaults")
        properties.forEach { prop ->
            val propName = prop.simpleName.asString()
            val propType = prop.type.resolve()
            val defaultValue = getDefaultValue(prop)
            val typeName = getTypeName(propType)

            if (typeName == SET.parameterizedBy(STRING)) {
                defaultsBuilder.addProperty(
                    PropertySpec.builder(propName, typeName)
                        .initializer(defaultValue)
                        .build()
                )
            } else {
                defaultsBuilder.addProperty(
                    PropertySpec.builder(propName, typeName)
                        .addModifiers(KModifier.CONST)
                        .initializer(defaultValue)
                        .build()
                )
            }
        }
        builder.addType(defaultsBuilder.build())

        builder.addFunction(buildToFunction())

        builder.addProperty(buildFlowProperty())

        builder.addFunction(buildUpdateFunction())

        return builder.build()
    }

    /**
     * 生成数据类流：IOException（文件损坏等）回退为空偏好，其余异常照抛
     *
     * Emits the data-class flow: an IOException (corrupt file and the like) falls back to
     * empty preferences, anything else rethrows.
     */
    private fun buildFlowProperty(): PropertySpec {
        val flowType = flowClass.parameterizedBy(ClassName(packageName, className))

        return PropertySpec.builder("flow", flowType)
            .receiver(dataStoreClass.parameterizedBy(preferencesClass))
            .getter(
                FunSpec.getterBuilder()
                    .addStatement(
                        """
                    return data
                        .catch { if (it is %T) emit(emptyPreferences()) else throw it }
                        .map { it.to${className}() }
                """.trimIndent(), ioExceptionClass
                    )
                    .build()
            )
            .build()
    }

    private fun buildToFunction(): FunSpec {
        val builder = FunSpec.builder("to${className}")
            .receiver(preferencesClass)
            .returns(ClassName(packageName, className))

        val constructorArgs = properties.joinToString(",\n    ") { prop ->
            val propName = prop.simpleName.asString()
            "$propName = this[$propName] ?: Defaults.$propName"
        }

        builder.addStatement(
            "return %T(\n    $constructorArgs\n)",
            ClassName(packageName, className)
        )
        return builder.build()
    }

    /**
     * 生成 update 写回：forceAll 时全量写回，否则读旧值只写差异项，
     * 避免为未变化的键触发 DataStore 写盘与监听
     *
     * Emits the update writer: forceAll writes every field back; otherwise the old values are
     * read and only differences are written, so unchanged keys cause neither a DataStore write
     * nor a listener emission.
     */
    private fun buildUpdateFunction(): FunSpec {
        val dataClass = ClassName(packageName, className)

        val forceAllBranch = properties.joinToString("\n            ") { prop ->
            val propName = prop.simpleName.asString()
            "prefs[$propName] = new.$propName"
        }

        val diffBranch = properties.joinToString("\n            ") { prop ->
            val propName = prop.simpleName.asString()
            """if (old.$propName != new.$propName) {
                prefs[$propName] = new.$propName
            }"""
        }

        return FunSpec.builder("update")
            .addModifiers(KModifier.SUSPEND)
            .receiver(dataStoreClass.parameterizedBy(preferencesClass))
            .addParameter("new", dataClass)
            .addParameter(
                ParameterSpec.builder("forceAll", BOOLEAN)
                    .defaultValue("false")
                    .build()
            )
            .addStatement(
                """
                edit { prefs ->
                    if (forceAll) {
                        $forceAllBranch
                    } else {
                        val old = prefs.to${className}()
                        $diffBranch
                    }
                }
            """.trimIndent()
            )
            .build()
    }

    private fun getKeyName(prop: KSPropertyDeclaration): String {
        val annotation = prop.annotations.find {
            it.shortName.asString() == "PrefKey"
        }
        val customName =
            annotation?.arguments?.find { it.name?.asString() == "name" }?.value as? String

        return if (customName.isNullOrEmpty()) {
            prop.simpleName.asString().camelToSnakeCase()
        } else {
            customName
        }
    }

    private fun getPreferencesKeyType(prop: KSPropertyDeclaration): TypeName {
        val type = prop.type.resolve()
        val keyClass = ClassName("androidx.datastore.preferences.core", "Preferences", "Key")
        return keyClass.parameterizedBy(getTypeName(type))
    }

    private fun getKeyFunctionName(prop: KSPropertyDeclaration): String {
        val type = prop.type.resolve()
        return when (val typeStr = type.declaration.simpleName.asString()) {
            "Boolean" -> "booleanPreferencesKey"
            "Int" -> "intPreferencesKey"
            "Long" -> "longPreferencesKey"
            "Float" -> "floatPreferencesKey"
            "Double" -> "doublePreferencesKey"
            "String" -> "stringPreferencesKey"
            "Set" -> "stringSetPreferencesKey"
            else -> {
                logger.error("Unsupported type: $typeStr for property ${prop.simpleName.asString()}")
                "stringPreferencesKey"
            }
        }
    }

    private fun getTypeName(type: KSType): TypeName {
        val typeStr = type.declaration.simpleName.asString()
        return when (typeStr) {
            "Boolean" -> BOOLEAN
            "Int" -> INT
            "Long" -> LONG
            "Float" -> FLOAT
            "Double" -> DOUBLE
            "String" -> STRING
            "Set" -> SET.parameterizedBy(STRING)
            else -> STRING
        }
    }

    private fun getDefaultValue(prop: KSPropertyDeclaration): String {
        val type = prop.type.resolve()
        val typeStr = type.declaration.simpleName.asString()

        val prefKeyAnnotation = prop.annotations.find { it.shortName.asString() == "PrefKey" }
        val prefStringSetAnnotation =
            prop.annotations.find { it.shortName.asString() == "PrefStringSet" }

        if (prefStringSetAnnotation != null) {
            val defaults = prefStringSetAnnotation.arguments
                .find { it.name?.asString() == "defaults" }
                ?.value as? List<*>
            return if (defaults.isNullOrEmpty()) {
                "emptySet()"
            } else {
                "setOf(${defaults.joinToString(", ") { "\"$it\"" }})"
            }
        }

        val defaultStr = prefKeyAnnotation?.arguments
            ?.find { it.name?.asString() == "default" }
            ?.value as? String ?: ""

        return when (typeStr) {
            "Boolean" -> defaultStr.ifEmpty { "false" }
            "Int" -> defaultStr.ifEmpty { "0" }
            "Long" -> if (defaultStr.isNotEmpty()) "${defaultStr}L" else "0L"
            "Float" -> if (defaultStr.isNotEmpty()) "${defaultStr}f" else "0f"
            "Double" -> defaultStr.ifEmpty { "0.0" }
            "String" -> "\"$defaultStr\""
            else -> "\"\""
        }
    }

    private fun String.camelToSnakeCase(): String {
        return this.replace(Regex("([a-z])([A-Z])")) { "${it.groupValues[1]}_${it.groupValues[2]}" }
            .lowercase()
    }
}

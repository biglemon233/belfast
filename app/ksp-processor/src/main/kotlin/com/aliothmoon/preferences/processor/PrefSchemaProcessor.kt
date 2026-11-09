package com.aliothmoon.preferences.processor

import com.aliothmoon.preferences.PrefSchema
import com.google.devtools.ksp.processing.CodeGenerator
import com.google.devtools.ksp.processing.KSPLogger
import com.google.devtools.ksp.processing.Resolver
import com.google.devtools.ksp.processing.SymbolProcessor
import com.google.devtools.ksp.symbol.KSAnnotated
import com.google.devtools.ksp.symbol.KSClassDeclaration
import com.google.devtools.ksp.validate

/**
 * 处理 @PrefSchema：收集类内带 @PrefKey 的属性，交给 [SchemaCodeGenerator] 产出 schema object
 *
 * Processes @PrefSchema: collects the @PrefKey properties inside each class and hands them to
 * [SchemaCodeGenerator] to emit the schema object.
 */
class PrefSchemaProcessor(
    private val codeGenerator: CodeGenerator,
    private val logger: KSPLogger
) : SymbolProcessor {

    /**
     * 校验未通过的符号原样返回，交由 KSP 下一轮再处理
     *
     * Returns symbols that fail validation untouched, for KSP to retry on a later round.
     */
    override fun process(resolver: Resolver): List<KSAnnotated> {
        val symbols = resolver.getSymbolsWithAnnotation(PrefSchema::class.qualifiedName!!)
        val unableToProcess = symbols.filterNot { it.validate() }.toList()

        symbols.filter { it is KSClassDeclaration && it.validate() }
            .forEach { symbol ->
                val classDecl = symbol as KSClassDeclaration
                processClass(classDecl)
            }

        return unableToProcess
    }

    private fun processClass(classDecl: KSClassDeclaration) {
        val packageName = classDecl.packageName.asString()
        val className = classDecl.simpleName.asString()

        val schemaAnnotation = classDecl.annotations
            .find { it.shortName.asString() == "PrefSchema" }
        val schemaName = schemaAnnotation?.arguments
            ?.find { it.name?.asString() == "name" }
            ?.value as? String ?: ""

        val properties = classDecl.getAllProperties()
            .filter { prop ->
                prop.annotations.any {
                    it.shortName.asString() == "PrefKey"
                }
            }
            .toList()

        if (properties.isEmpty()) {
            // 没有 @PrefKey 的 schema 生成不出任何访问器，多半是漏写注解而非有意为空
            logger.warn("No @PrefKey  properties found in $className")
            return
        }

        val generator = SchemaCodeGenerator(
            codeGenerator = codeGenerator,
            packageName = packageName,
            className = className,
            schemaName = schemaName.ifEmpty { className },
            properties = properties,
            logger = logger,
            originatingFile = classDecl.containingFile!!
        )
        generator.generate()
    }
}

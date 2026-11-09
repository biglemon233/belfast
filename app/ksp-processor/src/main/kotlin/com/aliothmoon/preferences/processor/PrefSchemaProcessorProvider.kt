package com.aliothmoon.preferences.processor

import com.google.devtools.ksp.processing.SymbolProcessor
import com.google.devtools.ksp.processing.SymbolProcessorEnvironment
import com.google.devtools.ksp.processing.SymbolProcessorProvider

/**
 * KSP 入口：把环境提供的 codeGenerator 与 logger 装配给 [PrefSchemaProcessor]
 *
 * KSP entry point: wires the environment-provided codeGenerator and logger into
 * [PrefSchemaProcessor].
 */
class PrefSchemaProcessorProvider : SymbolProcessorProvider {
    override fun create(environment: SymbolProcessorEnvironment): SymbolProcessor {
        return PrefSchemaProcessor(
            codeGenerator = environment.codeGenerator,
            logger = environment.logger
        )
    }
}

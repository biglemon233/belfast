package com.aliothmoon.maafw.gradle

import com.android.build.api.dsl.ApplicationExtension
import org.gradle.api.Project
import java.io.File

/** Android may select an ARM ABI for an x86 emulator via its native bridge.
 * Ship native executables for both hosts in either install-time library directory.
 * These files are exec'd as separate processes, never loaded into the app VM.
 */
internal fun Project.configureUniversalProot(android: ApplicationExtension) {
    val generated = layout.buildDirectory.dir("generated/universalProot")
    val sources = file("src/main/prootLibs")
    val variants = mapOf("arm64-v8a" to "arm64", "x86_64" to "x86_64")
    val names = listOf("libproot.so", "libproot-loader.so", "libtalloc.so", "libandroid-shmem.so")
    val prepare = tasks.register("prepareUniversalProot") {
        inputs.files(variants.keys.flatMap { abi -> names.map { File(sources, "$abi/$it") } })
        outputs.dir(generated)
        doLast {
            for ((abi, suffix) in variants) {
                // Short aliases fit the original DT_NEEDED strings without
                // changing dynstr offsets. Separate names avoid wrong-ISA deps.
                val talloc = if (abi == "x86_64") "libtx.so" else "libta.so"
                val shmem = if (abi == "x86_64") "libsx.so" else "libsa.so"
                for (name in names) {
                    val source = File(sources, "$abi/$name")
                    require(source.isFile) { "Missing PRoot input: $source" }
                    var bytes = source.readBytes()
                    if (name == "libproot.so") {
                        bytes = replaceNeeded(bytes, "libtalloc.so", talloc)
                        bytes = replaceNeeded(bytes, "libandroid-shmem.so", shmem)
                    }
                    val outputName = when (name) {
                        "libproot.so" -> "libproot_$suffix.so"
                        "libproot-loader.so" -> "libproot_loader_$suffix.so"
                        "libtalloc.so" -> talloc
                        else -> shmem
                    }
                    for (installAbi in variants.keys) {
                        val output = generated.get().file("$installAbi/$outputName").asFile
                        output.parentFile.mkdirs()
                        output.writeBytes(bytes)
                    }
                }
            }
        }
    }
    android.sourceSets.getByName("main").jniLibs.directories.add(generated.get().asFile.path)
    tasks.named("preBuild").configure { dependsOn(prepare) }
}

private fun replaceNeeded(bytes: ByteArray, old: String, replacement: String): ByteArray {
    val needle = (old + "\u0000").toByteArray(Charsets.US_ASCII)
    val value = replacement.toByteArray(Charsets.US_ASCII)
    require(value.size < needle.size)
    val offsets = (0..bytes.size - needle.size).filter { offset ->
        needle.indices.all { bytes[offset + it] == needle[it] }
    }
    require(offsets.size == 1) { "Unexpected DT_NEEDED string layout for $old" }
    val offset = offsets.single()
    bytes.fill(0, offset, offset + needle.size)
    value.copyInto(bytes, offset)
    return bytes
}

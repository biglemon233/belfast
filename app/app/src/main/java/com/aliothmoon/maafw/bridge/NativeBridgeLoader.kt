package com.aliothmoon.maafw.bridge

import android.os.Build
import android.os.Process
import com.aliothmoon.maafw.provision.RuntimeArchitecture
import dalvik.system.BaseDexClassLoader
import java.io.File
import java.nio.file.Files
import java.util.zip.ZipFile

/** Shizuku/root app_process uses the host ISA, even when the app uses ARM translation. */
internal object NativeBridgeLoader {
    @JvmStatic
    fun load(loader: ClassLoader) {
        try {
            System.loadLibrary("bridge")
            return
        } catch (error: UnsatisfiedLinkError) {
            // Only privileged processes may use a temporary executable mapping.
            // App processes continue to use their normal classloader/native bridge.
            if (Process.myUid() != 0 && Process.myUid() != Process.SHELL_UID) throw error
            val installed = (loader as? BaseDexClassLoader)?.findLibrary("bridge") ?: throw error
            val apk = if ("!/" in installed) File(installed.substringBefore("!/")) else {
                File(File(installed).canonicalFile.parentFile.parentFile.parentFile, "base.apk")
            }
            val abi = RuntimeArchitecture.deviceAbi(File("/system/bin/app_process64"), Build.SUPPORTED_ABIS.toList())
            val directory = Files.createTempDirectory(File("/data/local/tmp").toPath(), "alasaos-bridge-").toFile()
            val files = mutableListOf<File>()
            try {
                ZipFile(apk).use { zip ->
                    // Preload the same-ISA STL before bridge's DT_NEEDED lookup.
                    for (name in listOf("libc++_shared.so", "libbridge.so")) {
                        val entry = zip.getEntry("lib/$abi/$name") ?: throw error
                        val file = File(directory, name)
                        files += file
                        zip.getInputStream(entry).use { input -> file.outputStream().use { input.copyTo(it) } }
                        check(RuntimeArchitecture.elfArchitecture(file) == RuntimeArchitecture.linuxArchitecture(abi)) {
                            "Native bridge ELF does not match privileged process: $abi"
                        }
                        System.load(file.absolutePath)
                    }
                }
            } finally {
                // Loaded ELF mappings survive unlink; leave no privileged cache behind.
                files.forEach { it.delete() }
                directory.delete()
            }
        }
    }
}

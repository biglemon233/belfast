package com.aliothmoon.maafw.settings

import java.io.File
import java.io.FileOutputStream
import java.io.RandomAccessFile
import java.net.URI
import java.nio.charset.CodingErrorAction
import java.nio.file.Files
import java.nio.file.StandardCopyOption

/** Credentials may occur in repository URLs. Never use the generated data-class toString. */
class AlasSource(val repository: String, val branch: String) {
    override fun toString() = "AlasSource(<private>)"

    fun validated(): AlasSource {
        require(validRepository(repository) && validBranch(branch)) { "Invalid source configuration" }
        return this
    }

    companion object {
        const val DOMESTIC = "git://git.lyoko.io/AzurLaneAutoScript"
        const val GITHUB = "https://github.com/LmeSzinc/AzurLaneAutoScript"

        fun fromInput(repository: String, branch: String) = AlasSource(
            repository.trim().ifEmpty { DOMESTIC }, branch.trim().ifEmpty { "master" },
        ).validated()

        fun validBranch(value: String): Boolean = value.isNotEmpty() &&
            !value.startsWith('-') && !value.endsWith('.') && !value.contains("..") &&
            !value.contains("@{") && value.none { it.code <= 32 || it.code == 127 || it in "~^:?*[\\" } &&
            value.split('/').all { it.isNotEmpty() && !it.startsWith('.') && !it.endsWith(".lock") }

        fun validRepository(value: String): Boolean {
            if (value.isEmpty() || value.any { it.isWhitespace() || it.code < 32 || it.code == 127 }) return false
            if (value.startsWith('/')) return true // Local Git repositories are valid too.
            if (Regex("(?:[A-Za-z0-9._-]+@)?[A-Za-z0-9.-]+:[^/].+").matches(value) &&
                !value.contains("://") && !value.contains("::")) return true
            return runCatching {
                val uri = URI(value)
                uri.scheme in setOf("http", "https", "ssh", "git", "file") &&
                    (uri.scheme == "file" || !uri.host.isNullOrEmpty()) &&
                    !uri.rawPath.isNullOrEmpty() && uri.fragment == null && uri.port in -1..65535
            }.getOrDefault(false)
        }
    }
}

/** Deliberately limited to deploy's two scalar keys; never serialize the rest of YAML. */
internal object AlasDeployCodec {
    private val keyLine = Regex("(?m)^([ \\t]*)(Repository|Branch)(:[ \\t]*)([^\\r\\n]*)(\\r?)$")

    private data class Scalar(val value: String, val suffix: String, val quote: Char?)

    private fun scalar(raw: String): Scalar {
        var quote: Char? = null
        var comment = raw.length
        var i = 0
        while (i < raw.length) {
            val c = raw[i]
            if (quote == '\'' && c == '\'' && raw.getOrNull(i + 1) == '\'') { i += 2; continue }
            if (quote == '"' && c == '\\') { i += 2; continue }
            if (quote != null && c == quote) quote = null
            else if (quote == null && i == 0 && (c == '\'' || c == '"')) quote = c
            else if (quote == null && c == '#' && (i == 0 || raw[i - 1].isWhitespace())) { comment = i; break }
            i++
        }
        require(quote == null) { "Invalid deploy scalar" }
        val token = raw.substring(0, comment).trimEnd()
        val style = token.firstOrNull()?.takeIf { it == '\'' || it == '"' }
        val value = when (style) {
            '\'' -> { require(token.endsWith('\'') && token.length >= 2); token.substring(1, token.length - 1).replace("''", "'") }
            '"' -> {
                require(token.endsWith('"') && token.length >= 2)
                kotlinx.serialization.json.Json.decodeFromString<String>(token)
            }
            else -> token
        }
        return Scalar(value, raw.substring(token.length), style)
    }

    private fun fields(text: String, section: String = "Git",
                       keys: Set<String> = setOf("Repository", "Branch"),
                       allowMissing: Boolean = false): Map<String, MatchResult> {
        val mapping = Regex("""^( *)([A-Za-z_]\w*):[ \t]*(.*)$""")
        val parents = mutableListOf<Pair<Int, String>>()
        val positions = mutableSetOf<Int>()
        var offset = 0
        var scalarIndent: Int? = null
        var deployCount = 0
        var gitCount = 0
        text.splitToSequence('\n').forEach { raw ->
            val start = offset
            offset += raw.length + 1
            val line = raw.removeSuffix("\r").removePrefix("﻿")
            if (line.isBlank() || line.trimStart().startsWith('#')) return@forEach
            val indent = line.takeWhile { it == ' ' }.length
            if (scalarIndent?.let { indent > it } == true) return@forEach
            scalarIndent = null
            while (parents.isNotEmpty() && parents.last().first >= indent) parents.removeAt(parents.lastIndex)
            val match = mapping.matchEntire(line) ?: return@forEach
            val key = match.groupValues[2]
            val value = match.groupValues[3]
            val path = parents.map { it.second }
            if (path == listOf("Deploy", section) && key in keys) positions += start
            if (value.isBlank() || value.startsWith('#')) {
                if (key == "Deploy" && path.isEmpty()) deployCount++
                if (key == section && path == listOf("Deploy")) gitCount++
                parents += indent to key
            } else if (value.startsWith('|') || value.startsWith('>')) scalarIndent = indent
        }
        val pattern = if (section == "Git") keyLine
            else Regex("(?m)^([ \\t]*)(Run)(:[ \\t]*)([^\\r\\n]*)(\\r?)$")
        val matches = pattern.findAll(text).filter { it.range.first in positions }.toList()
        require(deployCount == 1 && gitCount == 1 &&
            (allowMissing || matches.size == keys.size) &&
            matches.map { it.groupValues[2] }.toSet().size == matches.size) { "Missing or ambiguous deploy field" }
        return matches.associateBy { it.groupValues[2] }
    }

    fun readRun(text: String): String {
        val match = fields(text, "Webui", setOf("Run"), allowMissing = true)["Run"] ?: return ""
        val value = scalar(match.groupValues[4])
        return if (value.quote == null && value.value in setOf("null", "~", "")) "" else value.value
    }

    fun replaceRun(text: String, config: String): String {
        require(config.isEmpty() || validRunConfig(config)) { "Invalid startup configuration" }
        val match = fields(text, "Webui", setOf("Run"), allowMissing = true)["Run"]
        val value = if (config.isEmpty()) "null" else "'$config'"
        if (match != null) {
            val old = scalar(match.groupValues[4])
            val replacement = match.groupValues[1] + "Run" + match.groupValues[3] + value +
                old.suffix + match.groupValues[5]
            return text.replaceRange(match.range, replacement)
        }
        val header = Regex("(?m)^([ ]*)Webui:[ \\t]*(?:#[^\\r\\n]*)?(\\r?)$").find(text)
            ?: error("Webui unavailable")
        val newline = if (text.contains("\r\n")) "\r\n" else "\n"
        return text.replaceRange(header.range, header.value.removeSuffix("\r") + newline +
            header.groupValues[1] + "  Run: $value" + header.groupValues[2])
    }

    fun validRunConfig(config: String): Boolean =
        config.isNotEmpty() &&
        !config.startsWith('-') &&
        !config.startsWith("template") &&
        config.none { it.isWhitespace() || it in ".\\/:*?\"'<>|\u0000" || it.isISOControl() }

    fun read(text: String): AlasSource {
        val fields = fields(text)
        return AlasSource(scalar(fields.getValue("Repository").groupValues[4]).value,
            scalar(fields.getValue("Branch").groupValues[4]).value).validated()
    }

    fun replace(text: String, source: AlasSource): String {
        source.validated()
        val positions = fields(text).values.map { it.range.first }.toSet()
        return keyLine.replace(text) { match ->
            if (match.range.first !in positions) return@replace match.value
            val old = scalar(match.groupValues[4])
            val value = if (match.groupValues[2] == "Repository") source.repository else source.branch
            if (old.value == value) match.value else {
                // Keep existing quotes where possible. Quote YAML-significant branch names.
                val quoted = when {
                    old.quote == '"' -> "\"" + value.replace("\\", "\\\\").replace("\"", "\\\"") + "\""
                    old.quote == '\'' || value.first() in "#&*!|>'\"%@`{[" ||
                        value.lowercase() in setOf("null", "true", "false", "yes", "no", "on", "off", "~") ||
                        value.toDoubleOrNull() != null -> "'" + value.replace("'", "''") + "'"
                    else -> value
                }
                match.groupValues[1] + match.groupValues[2] + match.groupValues[3] + quoted +
                    old.suffix + match.groupValues[5]
            }
        }
    }
}

/** Host-side deploy store. Call from IO, after provisioning/seed_deploy has completed.
 * The sidecar lock serializes AOS writers; revision checks reject edits from ALAS/another editor.
 * Non-cooperating external writers cannot participate in an atomic compare-and-swap on POSIX.
 */
class AlasSourceRepository(private val alasDir: File) {
    class Snapshot internal constructor(val source: AlasSource, internal val bytes: ByteArray) {
        override fun toString() = "AlasSourceSnapshot(<private>)"
    }
    class Conflict : IllegalStateException("Deploy changed; reload before saving")

    private val deploy get() = File(alasDir, "config/deploy.yaml")
    private val marker get() = File(alasDir, "config/.alasaos-source-v1")

    fun read(): Snapshot = locked { readUnlocked() }

    /** Only the untouched baked domestic/master pair is eligible for legacy preference migration. */
    fun migrateLegacy(repository: String, branch: String): Snapshot = locked {
        var snapshot = readUnlocked()
        if (!marker.exists()) {
            if (snapshot.source.repository == AlasSource.DOMESTIC && snapshot.source.branch == "master") {
                snapshot = saveUnlocked(snapshot, AlasSource.fromInput(repository, branch))
            }
            // Crash before this marker is safe: an already migrated custom deploy wins next time.
            atomicReplace(marker, "v1\n".toByteArray(), null)
        }
        snapshot
    }

    fun save(expected: Snapshot, source: AlasSource): Snapshot = locked {
        val saved = saveUnlocked(expected, source.validated())
        if (!marker.exists()) atomicReplace(marker, "v1\n".toByteArray(), null)
        saved
    }

    fun readTheme(): String = locked {
        if (!deploy.isFile || Files.isSymbolicLink(deploy.toPath())) return@locked "default"
        val text = decode(deploy.readBytes())
        val pattern = Regex("""(?m)^[ \t]*Theme:[ \t]*(\S+)""")
        val match = pattern.find(text)
        match?.groupValues?.get(1)?.trim('\'', '"') ?: "default"
    }

    /** ALAS owns Run; both its WebUI and AOS settings read the same deploy field. */
    fun readRunConfig(): String = locked {
        check(deploy.isFile && !Files.isSymbolicLink(deploy.toPath())) { "Deploy unavailable" }
        AlasDeployCodec.readRun(decode(deploy.readBytes()))
    }

    fun saveRunConfig(config: String) = locked {
        check(config.isEmpty() || config in runConfigs()) { "Configuration unavailable" }
        val current = deploy.readBytes()
        val bytes = AlasDeployCodec.replaceRun(decode(current), config).toByteArray(Charsets.UTF_8)
        if (!bytes.contentEquals(current)) atomicReplace(deploy, bytes, current)
    }

    fun runConfigs(): List<String> = File(alasDir, "config").listFiles().orEmpty()
        .filter { it.isFile && !Files.isSymbolicLink(it.toPath()) && it.extension == "json" &&
            AlasDeployCodec.validRunConfig(it.nameWithoutExtension) }
        .map { it.nameWithoutExtension }.sorted()

    fun updateTheme(theme: String): Boolean {
        require(theme == "dark" || theme == "default") { "Invalid theme: $theme" }
        return locked {
            if (!deploy.isFile || Files.isSymbolicLink(deploy.toPath())) return@locked false
            val currentBytes = deploy.readBytes()
            val text = decode(currentBytes)
            val pattern = Regex("""(?m)^([ \t]*Theme:[ \t]*)(\S+)(.*)$""")
            val match = pattern.find(text)
            val newText = if (match != null) {
                val current = match.groupValues[2].trim('\'', '"')
                if (current == theme) return@locked true
                pattern.replace(text) { "${it.groupValues[1]}$theme${it.groupValues[3]}" }
            } else {
                val webuiPattern = Regex("""(?m)^([ \t]*Webui:.*)$""")
                val webuiMatch = webuiPattern.find(text) ?: return@locked false
                val indent = webuiMatch.value.takeWhile { it == ' ' || it == '\t' } + "    "
                val lineBreak = if (text.contains("\r\n")) "\r\n" else "\n"
                webuiPattern.replace(text) { "${it.value}${lineBreak}${indent}Theme: $theme" }
            }
            val newBytes = newText.toByteArray(Charsets.UTF_8)
            atomicReplace(deploy, newBytes, currentBytes)
            true
        }
    }

    private fun saveUnlocked(expected: Snapshot, source: AlasSource): Snapshot {
        val current = readUnlocked()
        if (!current.bytes.contentEquals(expected.bytes)) throw Conflict()
        val bytes = AlasDeployCodec.replace(decode(current.bytes), source).toByteArray(Charsets.UTF_8)
        if (!bytes.contentEquals(current.bytes)) atomicReplace(deploy, bytes, current.bytes)
        return Snapshot(source, bytes)
    }

    private fun readUnlocked(): Snapshot {
        check(deploy.isFile && !Files.isSymbolicLink(deploy.toPath())) { "Deploy unavailable" }
        val bytes = deploy.readBytes()
        return Snapshot(AlasDeployCodec.read(decode(bytes)), bytes)
    }

    private fun decode(bytes: ByteArray): String = Charsets.UTF_8.newDecoder()
        .onMalformedInput(CodingErrorAction.REPORT).decode(java.nio.ByteBuffer.wrap(bytes)).toString()

    private fun <T> locked(block: () -> T): T = synchronized(processLock) {
        check(deploy.parentFile?.isDirectory == true) { "Deploy unavailable" }
        RandomAccessFile(File(deploy.parentFile, ".alasaos-source.lock"), "rw").use { file ->
            file.channel.lock().use { block() }
        }
    }

    private fun atomicReplace(target: File, bytes: ByteArray, expected: ByteArray?) {
        val temp = File.createTempFile(".alasaos-source-", ".tmp", target.parentFile)
        try {
            if (target.exists()) Files.copy(target.toPath(), temp.toPath(),
                StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.COPY_ATTRIBUTES)
            FileOutputStream(temp).use { it.write(bytes); it.fd.sync() }
            if (expected != null && (!target.isFile || Files.isSymbolicLink(target.toPath()) ||
                    !target.readBytes().contentEquals(expected))) throw Conflict()
            // Never fall back to a truncating/non-atomic write.
            Files.move(temp.toPath(), target.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
        } finally {
            temp.delete()
        }
    }

    private companion object { val processLock = Any() }
}

package com.aliothmoon.maafw.settings

import org.junit.Assert.*
import org.junit.Test

class AlasStartupSettingsTest {
    private val deploy = "# keep\r\nDeploy:\r\n  Git:\r\n    Repository: /repo\r\n    Branch: zenko\r\n" +
        "  Webui:\r\n    Run: null  # startup\r\n    Password: private\r\n"

    @Test fun selectingAndDisablingPreserveSourcePasswordCommentsAndLineEndings() {
        val selected = AlasDeployCodec.replaceRun(deploy, "qingao")
        assertEquals(deploy.replace("Run: null", "Run: 'qingao'"), selected)
        assertEquals("qingao", AlasDeployCodec.readRun(selected))
        assertEquals(deploy, AlasDeployCodec.replaceRun(selected, ""))
        assertEquals("", AlasDeployCodec.readRun(deploy))
    }

    @Test fun missingRunIsInsertedWithoutChangingOtherWebuiValues() {
        val original = deploy.replace("    Run: null  # startup\r\n", "")
        val updated = AlasDeployCodec.replaceRun(original, "alas")
        assertEquals("alas", AlasDeployCodec.readRun(updated))
        assertEquals(original.replace("  Webui:\r\n", "  Webui:\r\n    Run: 'alas'\r\n"), updated)
    }

    @Test fun unrelatedRunFieldsAndQuotedNullArePreserved() {
        val original = deploy + "Other:\r\n  Run: something\r\n"
        assertEquals(original.replace("Run: null", "Run: 'null'"), AlasDeployCodec.replaceRun(original, "null"))
        assertEquals("null", AlasDeployCodec.readRun(original.replace("Run: null", "Run: 'null'")))
    }

    @Test fun ambiguousWebuiOrRunIsRejected() {
        for (document in listOf(deploy.replace("    Run: null", "    Run: alas\r\n    Run: null"),
            deploy + "  Webui:\r\n    Run: other\r\n")) {
            assertThrows(IllegalArgumentException::class.java) { AlasDeployCodec.replaceRun(document, "alas") }
        }
    }

    @Test fun pathsArgumentsAndTemplateNamesCannotBecomeStartupConfigurations() {
        for (name in listOf("../alas", "--run", "alas qingao", "alas.json", "template", "alas\nBranch: other")) {
            assertThrows(IllegalArgumentException::class.java) { AlasDeployCodec.replaceRun(deploy, name) }
        }
    }

    @Test fun unicodeAndChineseConfigurationsAreSupported() {
        for (name in listOf("B服小开", "碧蓝航线", "alas_2", "config-1")) {
            val updated = AlasDeployCodec.replaceRun(deploy, name)
            assertEquals(name, AlasDeployCodec.readRun(updated))
        }
    }
}

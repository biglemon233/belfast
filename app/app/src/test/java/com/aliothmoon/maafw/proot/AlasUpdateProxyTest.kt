package com.aliothmoon.maafw.proot

import org.junit.Assert.*
import org.junit.Test

class AlasUpdateProxyTest {
    @Test fun staticProxyMergesBothNoProxyVariantsWithoutChangingInheritedMap() {
        val inherited = mapOf("NO_PROXY" to "*.keep,192.0.2.0/24", "no_proxy" to "second.invalid",
            "OTHER" to "untouched")
        val env = AlasUpdateProxy.staticEnvironment("127.0.0.1", 8080, false,
            listOf("excluded.invalid"), inherited)
        assertEquals("http://127.0.0.1:8080", env["http_proxy"])
        assertEquals(env["http_proxy"], env["HTTPS_PROXY"])
        assertEquals("*.keep,192.0.2.0/24,second.invalid,excluded.invalid,localhost,127.0.0.1,::1", env["NO_PROXY"])
        assertEquals(env["NO_PROXY"], env["no_proxy"])
        assertEquals("*.keep,192.0.2.0/24", inherited["NO_PROXY"])
        assertFalse(env.containsKey("OTHER"))
        assertFalse(env.keys.any { it.contains("SSL") || it.contains("PIP") || it.contains("ALL_PROXY") })
    }

    @Test fun emptyExclusionEntriesDoNotDisableAValidStaticProxy() {
        assertEquals("http://localhost:8080", AlasUpdateProxy.staticEnvironment(
            "localhost", 8080, false, listOf("", "  "), emptyMap())["http_proxy"])
    }

    @Test fun ipv6IsBracketed() {
        val env = AlasUpdateProxy.staticEnvironment("::1", 8080, false, emptyList(), emptyMap())
        assertEquals("http://[::1]:8080", env["https_proxy"])
    }

    @Test fun pacIsNeverInterpretedAsStaticEvenWithHostAndPort() {
        assertTrue(AlasUpdateProxy.staticEnvironment("localhost", 8080, true, emptyList(), emptyMap()).isEmpty())
    }

    @Test fun unsupportedExclusionsFailClosedRatherThanDroppingBypassRules() {
        for (rule in listOf("*.example.invalid", "<local>", "192.0.2.0/24", "*")) {
            assertTrue(AlasUpdateProxy.staticEnvironment("localhost", 8080, false, listOf(rule), emptyMap()).isEmpty())
        }
    }

    @Test fun invalidOrAbsentProxyDoesNotOverrideInheritedEnvironment() {
        for ((host, port) in listOf(null to 8080, "" to 8080, "host" to 0, "host" to 65536,
                                   "u:password@host" to 8080, "host/path" to 8080, "host\n" to 8080)) {
            assertTrue(AlasUpdateProxy.staticEnvironment(host, port, false, emptyList(), emptyMap()).isEmpty())
        }
    }
}

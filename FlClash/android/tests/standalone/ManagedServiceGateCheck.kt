package com.follow.clash

import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking

fun main() = runBlocking {
    var checks = 0
    val denied = listOf(
        null,
        "",
        "[]",
        "{",
        "x".repeat(65_537),
        """{"id":"wrong","result":{"can_connect":true}}""",
        """{"id":"managed-service-gate","error":{},"result":{"can_connect":true}}""",
        """{"id":"managed-service-gate","result":{"can_connect":"true"}}""",
        """{"id":"managed-service-gate","result":{"can_connect":false}}""",
        """{"id":"managed-service-gate","result":{}}""",
    )
    for (response in denied) {
        val gate = ManagedServiceGate { request, callback ->
            check(request.contains("managedStatus") && !request.contains("password"))
            callback(response)
        }
        check(!gate.allowed())
        checks++
    }
    val allowed = """{"id":"managed-service-gate","result":{"can_connect":true}}"""
    check(ManagedServiceGate { _, callback -> callback(allowed); callback(allowed) }.allowed())
    checks++
    check(!ManagedServiceGate { _, _ -> error("private-native-error") }.allowed())
    checks++
    check(!ManagedServiceGate { _, _ -> }.allowed())
    checks++
    var callback: ((String?) -> Unit)? = null
    val pending = launch(start = CoroutineStart.UNDISPATCHED) {
        ManagedServiceGate { _, cb -> callback = cb }.allowed()
    }
    pending.cancelAndJoin()
    callback!!(allowed)
    checks++
    println("PASS: $checks managed Android gate checks (standalone JVM; not an APK test)")
}

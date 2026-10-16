package com.asterlink.vpn

/**
 * Marks an instrumented test that is a suite's driver (run by class from
 * scripts/test-native-android.py with the world it needs). The emulator suite's
 * connectedDebugAndroidTest excludes them: the reporter counts their "no world" skip as a failure.
 */
@Retention(AnnotationRetention.RUNTIME)
@Target(AnnotationTarget.CLASS)
annotation class SuiteDriver

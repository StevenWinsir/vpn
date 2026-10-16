import com.asterlink.build.ClientEnvSource
import java.security.MessageDigest

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    id("asterlink.client-env")
}

val clientEnv = extensions.getByType<ClientEnvSource>()
val appVersion = providers.gradleProperty("asterlink.appVersion").get()
val appVersionCode = providers.gradleProperty("asterlink.versionCode").get().toInt()

// The Go Core (native/core + pinned Mihomo) is bound by gomobile into an AAR that
// scripts/build-native-android.py writes here together with its build record. Gradle never
// runs gomobile itself; it only checks that the AAR on disk is the one the record describes.
val coreDir = rootProject.layout.buildDirectory.dir("core")
val coreAar = coreDir.map { it.file("asterlink-core.aar") }
val coreRecord = coreDir.map { it.file("core-build.json") }

// The TUN acceptance build (scripts/test-native-android.py --suite tun): its Core AAR carries
// debug-only engine hooks (native/core/mobile/tuntest.go, -tags asterlink_tuntest) and its test
// APK adds src/androidTestTun. Both must be asked for together; verifyCoreAar refuses any mix,
// so an ordinary or release APK can never be built on a hooks AAR.
val tunTest = providers.gradleProperty("asterlink.tunTest").orNull == "true"

android {
    namespace = "com.asterlink.vpn"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.asterlink.vpn"
        minSdk = 26
        targetSdk = 36
        versionCode = appVersionCode
        versionName = appVersion
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"

        // Public build parameters (apps/android/.env, env-v1 rules). The Core receives them in
        // the login command and validates them again; nothing else is configurable at run time.
        buildConfigField("String", "APP_ENV", "\"${clientEnv.env.channel}\"")
        buildConfigField("String", "API_BASE_URL", "\"${clientEnv.env.apiBaseUrl}\"")
        buildConfigField("String", "WEBSITE_BASE_URL", "\"${clientEnv.env.websiteBaseUrl}\"")
        buildConfigField("boolean", "ENV_FROM_EXAMPLE", "${clientEnv.fromExample}")
    }

    buildTypes {
        debug {
            isMinifyEnabled = false
        }
        release {
            // Round 6: R8, gomobile keep rules, ABI splits and the user's signing key.
            isMinifyEnabled = false
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    packaging {
        // libgojni.so stays uncompressed and page-aligned in the APK (16 KB pages).
        jniLibs.useLegacyPackaging = false
    }

    sourceSets {
        // The shared contract fixtures, readable by the instrumented tests on the device.
        getByName("androidTest").assets.directories.add(rootDir.resolve("../../shared/contracts").path)
        if (tunTest) getByName("androidTest").kotlin.directories.add("src/androidTestTun/java")
    }

    testOptions {
        unitTests {
            // Robolectric reads the merged resources (strings, drawables, themes).
            isIncludeAndroidResources = true
            all {
                it.systemProperty("asterlink.contracts", rootDir.resolve("../../shared/contracts").absolutePath)
                // Screenshot tests always write their PNGs (there are no committed goldens to
                // compare against: fonts differ between hosts); the suite collects them.
                it.systemProperty("roborazzi.test.record", "true")
                it.systemProperty("asterlink.screenshots", layout.buildDirectory.dir("outputs/roborazzi").get().asFile.absolutePath)
                it.maxHeapSize = "3g"
            }
        }
    }
}

val verifyCoreAar = tasks.register("verifyCoreAar") {
    description = "Checks that the gomobile Core AAR exists and matches its build record."
    val aar = coreAar
    val record = coreRecord
    val wantHooks = tunTest
    val patchManifest = rootDir.resolve("../../native/third_party/patches/manifest.json")
    inputs.file(patchManifest)
    inputs.files(aar, record).optional()
    inputs.property("tunTest", wantHooks)
    doLast {
        val aarFile = aar.get().asFile
        val recordFile = record.get().asFile
        if (!aarFile.isFile || !recordFile.isFile) {
            throw GradleException(
                "Core AAR missing. Build it first: python3 scripts/build-native-android.py --target aar",
            )
        }
        val digest = MessageDigest.getInstance("SHA-256").digest(aarFile.readBytes()).joinToString("") { "%02x".format(it) }
        val recorded = Regex("\"aar_sha256\"\\s*:\\s*\"([0-9a-f]{64})\"").find(recordFile.readText())?.groupValues?.get(1)
        if (recorded != digest) {
            throw GradleException("Core AAR does not match core-build.json (sha256 $digest, recorded $recorded); rebuild it")
        }
        val patchHash = Regex("\"patch_sha256\"\\s*:\\s*\"([0-9a-f]{64})\"")
        val requiredPatch = patchHash.find(patchManifest.readText())?.groupValues?.get(1)
        val recordedPatch = patchHash.find(recordFile.readText())?.groupValues?.get(1)
        if (requiredPatch == null || recordedPatch != requiredPatch) {
            throw GradleException("Core AAR lacks the current Mihomo lifecycle patch; rebuild it with --rebuild-core")
        }
        val hooks = Regex("\"tuntest_hooks\"\\s*:\\s*true").containsMatchIn(recordFile.readText())
        if (hooks != wantHooks) {
            throw GradleException(
                if (hooks) "This Core AAR carries the TUN acceptance hooks; rebuild it without --tun-test-hooks"
                else "The TUN acceptance build needs a Core AAR bound with --tun-test-hooks",
            )
        }
    }
}

val refuseReleaseFromExample = tasks.register("refuseReleaseFromExample") {
    description = "Release builds need apps/android/.env; the public sample is for Debug only."
    val fromExample = clientEnv.fromExample
    doLast {
        if (fromExample) throw GradleException("Release builds read apps/android/.env, which is missing")
    }
}

tasks.named("preBuild") { dependsOn(verifyCoreAar) }
afterEvaluate {
    tasks.named("preReleaseBuild") { dependsOn(refuseReleaseFromExample) }
}

dependencies {
    implementation(files(coreAar))

    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.androidx.lifecycle.process)
    implementation(libs.androidx.datastore.preferences)
    implementation(libs.androidx.browser)
    implementation(libs.androidx.core.splashscreen)
    debugImplementation(libs.compose.ui.tooling)
    // Hosts the empty activity that createComposeRule() launches (JVM screenshot tests).
    debugImplementation(libs.compose.ui.test.manifest)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    // Screenshot tests on the JVM (Robolectric native graphics + Roborazzi), no emulator.
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    testImplementation(libs.robolectric)
    testImplementation(libs.roborazzi)
    testImplementation(libs.roborazzi.compose)

    androidTestImplementation(libs.androidx.test.ext.junit)
    androidTestImplementation(libs.androidx.test.runner)
}

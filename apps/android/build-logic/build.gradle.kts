plugins {
    `kotlin-dsl`
}

dependencies {
    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.serialization.json)
}

gradlePlugin {
    plugins {
        register("clientEnv") {
            id = "asterlink.client-env"
            implementationClass = "com.asterlink.build.ClientEnvPlugin"
        }
    }
}

tasks.test {
    // The same fixture the Python, Swift and C# parsers are pinned to.
    systemProperty("asterlink.contracts", rootDir.resolve("../../../shared/contracts").absolutePath)
}

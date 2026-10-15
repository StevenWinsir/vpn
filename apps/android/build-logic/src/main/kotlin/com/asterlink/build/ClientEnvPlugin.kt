package com.asterlink.build

import org.gradle.api.GradleException
import org.gradle.api.Plugin
import org.gradle.api.Project

/**
 * Parses apps/android/.env once per build and exposes it as the `clientEnv` extension.
 * Without a .env the public sample (.env.example) is used, and [ClientEnvSource.fromExample]
 * says so; release builds refuse that (app/build.gradle.kts), exactly like the macOS and
 * Windows build scripts.
 */
class ClientEnvPlugin : Plugin<Project> {
    override fun apply(project: Project) {
        val root = project.rootProject.layout.projectDirectory
        val env = root.file(".env").asFile
        val file = if (env.exists()) env else root.file(".env.example").asFile
        val parsed = try {
            ClientEnv.load(file)
        } catch (error: ClientEnv.Invalid) {
            throw GradleException("apps/android/${file.name} is invalid: ${error.message}")
        }
        project.extensions.add("clientEnv", ClientEnvSource(parsed, fromExample = file != env))
    }
}

data class ClientEnvSource(val env: ClientEnv, val fromExample: Boolean)

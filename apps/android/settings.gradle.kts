pluginManagement {
    // Build conventions owned by this repository: the .env parser (env-v1 rules) and the
    // Core AAR check.
    includeBuild("build-logic")
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "AsterLink"
include(":app")

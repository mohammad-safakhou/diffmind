// Minimal Kotlin/JVM build for the SCIP integration fixture.
// We intentionally avoid Spring Boot or other heavy frameworks: the
// integration test only cares about resolving symbols and walking
// the call graph through controller → service → repository, which a
// plain kotlin-jvm project gives us at zero CI cost.

plugins {
    kotlin("jvm") version "2.1.20"
}

repositories {
    mavenCentral()
}

dependencies {
    // Kotlin stdlib is added transitively by the plugin.
}

kotlin {
    jvmToolchain(21)
}

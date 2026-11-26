// The Kotlin of the native module androidcore: an Android library, which mygo build adds to the
// app's Gradle build, carrying libandrofuse_core.so (scripts/build-android-core.sh) in its
// src/main/jniLibs/<abi>/.
plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.github.yuanzhe_99.androfuse.internal.androidcore"
    packaging { jniLibs { useLegacyPackaging = false } }
    defaultConfig {
        // Keep only the ABIs Go was built for (android/arm64).
        providers.gradleProperty("mygoAbis").orNull?.split(',')?.filter { it.isNotEmpty() }?.let { abis ->
            ndk.abiFilters.addAll(abis)
        }
    }
    sourceSets {
        getByName("main") {
            jniLibs.setSrcDirs(emptyList<File>())
        }
    }
}

// Stage only the ABIs requested by mygoAbis into a clean jniLibs directory, so an arm64 APK
// never bundles another ABI's core.
val stageJniLibs = tasks.register<Sync>("stageAndroFuseJniLibs") {
    val abis = providers.gradleProperty("mygoAbis").orNull?.split(',')?.filter { it.isNotEmpty() }
    from("src/main/jniLibs") {
        if (!abis.isNullOrEmpty()) {
            abis.forEach { include("$it/**") }
        }
    }
    into(layout.buildDirectory.dir("stagedJniLibs"))
}

android.sourceSets.getByName("main").jniLibs.srcDir(stageJniLibs.map { it.destinationDir })

tasks.matching { it.name.contains("JniLibFolders") }.configureEach {
    dependsOn(stageJniLibs)
}

dependencies {
}

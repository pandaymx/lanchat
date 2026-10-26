import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    alias(libs.plugins.android.application)
    // AGP 9.0 起内置 Kotlin 支持，org.jetbrains.kotlin.android 插件
    // 不再需要，显式应用反而会构建失败；Compose 编译器插件仍需保留。
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "dev.lanchat"
    // androidx.navigation 2.10+ 等依赖要求 compileSdk >= 37
    compileSdk = 37

    defaultConfig {
        applicationId = "dev.lanchat"
        minSdk = 26
        targetSdk = 36
        versionCode = 24
        versionName = "2.0.0"
        ndk {
            abiFilters += listOf("arm64-v8a")
        }
    }

    buildTypes {
        release {
            // 关闭 R8 激进裁剪，避免 gomobile JNI / Compose 反射符号被误删引发运行期闪退；
            // APK 体积主要由 useLegacyPackaging (Deflate 压缩 libgojni.so) 与 arm64 单架构控制。
            isMinifyEnabled = false
            isShrinkResources = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
    }

    packaging {
        jniLibs {
            // 将 libgojni.so 以 Deflate 压缩打入 APK，避免未压缩存储占 ~14 MB
            useLegacyPackaging = true
        }
        resources {
            excludes += "/META-INF/{AL20.0,LGPL2.1}"
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
}

dependencies {
    // gomobile 产物：由 Taskfile 目标 android:aar 生成并放入 app/libs。
    implementation(fileTree(mapOf("dir" to "libs", "include" to listOf("*.aar"))))

    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.activity.compose)
    implementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.ui)
    implementation(libs.androidx.ui.graphics)
    implementation(libs.androidx.ui.tooling.preview)
    implementation(libs.androidx.material3)
    implementation(libs.androidx.material.icons.extended)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.datastore.preferences)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.androidx.documentfile)

    debugImplementation(libs.androidx.ui.tooling)

    testImplementation(libs.junit)
    // 纯 JVM 版 org.json：生产代码使用 Android 内置同名 API，测试借此无需设备即可运行。
    testImplementation(libs.org.json)
}

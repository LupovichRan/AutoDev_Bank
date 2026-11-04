plugins {
    id("com.android.application")
}

android {
    namespace = "bank.autodev.shell"
    compileSdk = 35

    defaultConfig {
        applicationId = "bank.autodev.shell"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

// Deliberately no dependencies: the shell is plain android.webkit + android.app.

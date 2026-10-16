# R8 rules for the release APK (handoff_app_android.md round 6, docs/android-release.md).
#
# The gomobile binding: libgojni.so finds these classes, their constructors, fields and methods by
# name through JNI (FindClass / GetMethodID in the generated seq code), so nothing in them may be
# renamed or removed. `go.*` is gomobile's runtime (Seq, Universe, error proxies);
# com.asterlink.core.* is the generated binding of native/core/mobile (javapkg com.asterlink.core).
-keep class go.** { *; }
-keep interface go.** { *; }
-keep class com.asterlink.core.** { *; }
-keep interface com.asterlink.core.** { *; }

# The Kotlin object the Go side calls back (Protect, OpenTun, NetworkSnapshot): it implements the
# kept interface com.asterlink.core.mobile.Platform; keep the implementations of its methods by name.
-keepclassmembers class * implements com.asterlink.core.mobile.Platform { *; }

# Stack traces in a crash report stay readable against build/outputs/mapping/release/mapping.txt
# (kept next to the release, never shipped): keep file and line numbers, hide the source file name.
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile

# kotlinx.serialization ships its own R8 rules in the library; the @Serializable classes of
# core/Protocol.kt and core/Snapshot.kt need nothing more here.

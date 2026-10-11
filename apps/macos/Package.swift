// swift-tools-version: 6.0
import PackageDescription

// SwiftPM (not an .xcodeproj) is the single build description so that CI and local
// builds share one definition. scripts/build-native-macos.py assembles, signs and
// verifies the .app bundle around the products below.
let package = Package(
    name: "AsterLink",
    platforms: [.macOS(.v15)],
    products: [
        .executable(name: "AsterLink", targets: ["AsterLinkApp"]),
        .executable(name: "AsterLinkHelper", targets: ["AsterLinkHelper"]),
        .library(name: "AsterLinkShared", targets: ["AsterLinkShared"]),
    ],
    targets: [
        .target(name: "AsterLinkShared"),
        .executableTarget(name: "AsterLinkHelper", dependencies: ["AsterLinkShared"]),
        .executableTarget(name: "AsterLinkApp", dependencies: ["AsterLinkShared"]),
        .testTarget(
            name: "AsterLinkSharedTests",
            dependencies: ["AsterLinkShared"],
            resources: []
        ),
    ],
    swiftLanguageModes: [.v5]
)

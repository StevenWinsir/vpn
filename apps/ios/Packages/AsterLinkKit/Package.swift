// swift-tools-version: 6.0
import PackageDescription

// Business logic shared by the iOS app and its tunnel extension (handoff_app_ios.md 3.1).
// macOS is listed only so `swift test` runs on the development Mac without a simulator.
// CAsterResolv reads the system resolver list (libresolv has no Swift module).
let package = Package(
    name: "AsterLinkKit",
    platforms: [.iOS(.v17), .macOS(.v15)],
    products: [.library(name: "AsterLinkKit", targets: ["AsterLinkKit"])],
    targets: [
        .target(name: "CAsterResolv", linkerSettings: [.linkedLibrary("resolv")]),
        .target(name: "AsterLinkKit", dependencies: ["CAsterResolv"]),
        .testTarget(name: "AsterLinkKitTests", dependencies: ["AsterLinkKit"]),
    ]
)

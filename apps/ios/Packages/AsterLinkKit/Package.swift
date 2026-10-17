// swift-tools-version: 6.0
import PackageDescription

// Business logic shared by the iOS app and its tunnel extension (handoff_app_ios.md 3.1).
// macOS is listed only so `swift test` runs on the development Mac without a simulator.
let package = Package(
    name: "AsterLinkKit",
    platforms: [.iOS(.v17), .macOS(.v15)],
    products: [.library(name: "AsterLinkKit", targets: ["AsterLinkKit"])],
    targets: [
        .target(name: "AsterLinkKit"),
        .testTarget(name: "AsterLinkKitTests", dependencies: ["AsterLinkKit"]),
    ]
)

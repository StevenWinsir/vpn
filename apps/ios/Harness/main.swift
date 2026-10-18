import AsterCore
import Foundation

// The simulator-side runner of the billing acceptance (handoff_app_ios.md 8.8). Not part of the
// app and not referenced by the Xcode project: scripts/test-native-ios.py --suite billing compiles
// it for the iOS Simulator against the TEST-ONLY harness framework (.runtime/ios/harness, Go tag
// asterlink_harness) and runs it with `simctl spawn`, so the Core that carries the traffic is the
// iOS-simulator build, in a simulator process. Usage: harness-runner <config.json> <report.json>.
// The config names the backend world, the sink and the sizes; nothing else is read.

let arguments = CommandLine.arguments
guard arguments.count == 3, let config = try? String(contentsOfFile: arguments[1], encoding: .utf8) else {
    FileHandle.standardError.write(Data("usage: harness-runner <config.json> <report.json>\n".utf8))
    exit(2)
}
let report = MobileHarnessRun(config)
do {
    try report.write(toFile: arguments[2], atomically: true, encoding: .utf8)
} catch {
    FileHandle.standardError.write(Data("cannot write the report\n".utf8))
    exit(2)
}
print("harness-runner: report written (\(report.utf8.count) bytes)")

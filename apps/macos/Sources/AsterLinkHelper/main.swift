import AsterLinkShared
import Darwin
import Foundation
import SystemConfiguration

// Build identity is read from the Info.plist embedded in this executable by the
// signed build (scripts/build-native-macos.py), never from the environment or argv.
let info = Bundle.main.infoDictionary ?? [:]
let helperVersion = (info["CFBundleShortVersionString"] as? String) ?? "0.0.0-dev"
let channel = BuildChannel(rawValue: (info["AsterLinkChannel"] as? String) ?? "development") ?? .development
let teamID = (info["AsterLinkTeamID"] as? String).flatMap { $0.isEmpty ? nil : $0 }

func executableDirectory() -> URL {
    var size: UInt32 = 0
    _NSGetExecutablePath(nil, &size)
    var buffer = [CChar](repeating: 0, count: Int(size))
    _NSGetExecutablePath(&buffer, &size)
    return URL(fileURLWithPath: String(cString: buffer)).resolvingSymlinksInPath().deletingLastPathComponent()
}

guard let policy = try? SigningPolicy(channel: channel, teamID: teamID) else {
    FileHandle.standardError.write(Data("invalid signing policy\n".utf8))
    exit(78)
}
// <App>.app/Contents/MacOS/AsterLinkHelper -> <App>.app/Contents/Resources
let resources = executableDirectory().deletingLastPathComponent().appendingPathComponent("Resources")

#if DEBUG
// Debug-only, never compiled into Release: exercises verify + install + spawn + IPC hello
// as the current user, without XPC, launchd or root. `--self-test-root` must be a fresh dir.
if CommandLine.arguments.count == 3, CommandLine.arguments[1] == "--self-test-root" {
    let store = WorkerStore(root: URL(fileURLWithPath: CommandLine.arguments[2]), requiredOwner: getuid())
    let supervisor = WorkerSupervisor(store: store, policy: policy, bundleResources: resources)
    do {
        let hello = try supervisor.start()
        let running = supervisor.isRunning
        // A second start must be refused deterministically.
        var secondRefused = false
        do { _ = try supervisor.start() } catch let failure as HelperFailure { secondRefused = failure.code == .workerBusy } catch {}
        supervisor.stop()
        let reply = HelperReply(ok: running && secondRefused, helperVersion: helperVersion, workerRunning: supervisor.isRunning, core: hello)
        print(String(decoding: reply.encoded(), as: UTF8.self))
        exit(running && secondRefused ? 0 : 1)
    } catch let failure as HelperFailure {
        print(String(decoding: HelperReply(ok: false, errorCode: failure.code, detail: failure.detail, helperVersion: helperVersion, workerRunning: false).encoded(), as: UTF8.self))
        exit(1)
    } catch { exit(1) }
}
#endif

guard getuid() == 0 else {
    FileHandle.standardError.write(Data("AsterLinkHelper must run as the launchd daemon (root)\n".utf8))
    exit(77)
}

let store = WorkerStore(root: URL(fileURLWithPath: "/Library/Application Support/AsterLink/Workers"))
let supervisor = WorkerSupervisor(store: store, policy: policy, bundleResources: resources)
let delegate: ListenerDelegate
do {
    delegate = ListenerDelegate(requirement: try policy.requirement(forIdentifier: AsterLinkIdentity.appBundleID),
                                supervisor: supervisor, helperVersion: helperVersion)
} catch {
    FileHandle.standardError.write(Data("cannot build caller requirement\n".utf8))
    exit(78)
}
let listener = NSXPCListener(machServiceName: AsterLinkIdentity.helperMachService)
listener.delegate = delegate
listener.resume()
RunLoop.main.run()

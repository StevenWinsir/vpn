import AsterLinkShared
import SwiftUI

/// App entry. Closing the window keeps the app in the menu bar; the menu's Quit (or ⌘Q)
/// stops the Core, which revokes the session before the process exits.
@main
struct AsterLinkApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    private let model = AppModel.make()

    var body: some Scene {
        WindowGroup("AsterLink", id: "main") {
            if let session = model.session {
                RootView(session: session, helper: model.helper, diagnostics: model.diagnostics)
                    .onAppear { delegate.session = session }
            } else {
                ConfigurationErrorView()
            }
        }
        .windowResizability(.contentMinSize)

        MenuBarExtra("AsterLink", systemImage: "bolt.shield") {
            MenuContent(session: model.session)
        }
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    var session: SessionController?

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let session else { return .terminateNow }
        Task {
            await session.shutdown()
            NSApp.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }
}

struct MenuContent: View {
    let session: SessionController?
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        if let session {
            Text(session.phase == .signedIn ? (session.snapshot.user?.email ?? "已登录") : "未登录")
        }
        Button("打开 AsterLink") {
            openWindow(id: "main")
            NSApp.activate(ignoringOtherApps: true)
        }
        Divider()
        Button("退出") { NSApp.terminate(nil) }.keyboardShortcut("q")
    }
}

struct ConfigurationErrorView: View {
    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: "exclamationmark.octagon").font(.largeTitle).foregroundStyle(.red)
            Text("应用配置缺失或无效，已拒绝启动。").font(.headline)
            Text("请重新安装官方版本。").foregroundStyle(.secondary)
        }.padding(40)
    }
}

/// Wires the real collaborators once. A missing or invalid build configuration yields no
/// session at all: there is no default server to fall back to.
@MainActor
struct AppModel {
    let session: SessionController?
    let helper: HelperClient
    let diagnostics: AppDiagnostics

    static func make() -> AppModel {
        let info = Bundle.main.infoDictionary ?? [:]
        let channel = BuildChannel(rawValue: (info["AsterLinkChannel"] as? String) ?? "development") ?? .development
        let team = (info["AsterLinkTeamID"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        let policy = (try? SigningPolicy(channel: channel, teamID: team)) ?? (try! SigningPolicy(channel: .development, teamID: nil))
        let connection = HelperConnection(policy: policy)
        let helper = HelperClient(connection: connection)

        var environment: ClientEnvironment?
        if let api = info["AsterLinkAPIBase"] as? String, let site = info["AsterLinkWebsiteBase"] as? String {
            // Re-validated with the same rules as the build script; the plist is not trusted blindly.
            environment = try? ClientEnvironment.parse("APP_ENV=\(channel.rawValue)\nAPI_BASE_URL=\(api)\nWEBSITE_BASE_URL=\(site)\n")
        }
        #if DEBUG
        if environment == nil, channel == .development, let api = ProcessInfo.processInfo.environment["ASTERLINK_DEV_API"],
           let site = ProcessInfo.processInfo.environment["ASTERLINK_DEV_SITE"] {
            environment = try? ClientEnvironment.parse("APP_ENV=development\nAPI_BASE_URL=\(api)\nWEBSITE_BASE_URL=\(site)\n")
        }
        #endif
        let diagnostics = AppDiagnostics(environment: environment)
        guard let environment else { return AppModel(session: nil, helper: helper, diagnostics: diagnostics) }

        var transport: CoreTransport = XPCCoreTransport(connection: connection, appVersion: diagnostics.appVersion)
        #if DEBUG
        if channel == .development, let path = ProcessInfo.processInfo.environment["ASTERLINK_DEV_CORE"] {
            transport = DirectCoreTransport(executable: URL(fileURLWithPath: path))
        }
        #endif
        let client = CoreClient(transport: transport, environment: environment, appVersion: diagnostics.appVersion)
        let session = SessionController(client: client, environment: environment, credentials: KeychainCredentialStore(),
                                        preferences: UserDefaultsLoginPreferences(), devices: KeychainDeviceIdentity())
        return AppModel(session: session, helper: helper, diagnostics: diagnostics)
    }
}

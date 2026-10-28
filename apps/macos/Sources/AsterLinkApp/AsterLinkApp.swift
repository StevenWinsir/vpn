import AsterLinkShared
import SwiftUI

@main
enum Launcher {
    @MainActor
    static func main() {
        #if DEBUG
        // Design preview (handoff_app_standardization.md 5.10): renders every scenario to PNG and exits.
        if let index = CommandLine.arguments.firstIndex(of: "--preview"), index + 1 < CommandLine.arguments.count {
            PreviewRunner.run(output: URL(fileURLWithPath: CommandLine.arguments[index + 1]))
            return
        }
        #endif
        AsterLinkApp.main()
    }
}

/// App entry. One main window and a menu bar panel. Closing the window leaves the app running in
/// the menu bar without a Dock icon; Quit (menu bar panel, ⌘Q, the account page) stops the Core,
/// which revokes the session before the process exits.
struct AsterLinkApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    private let model = AppModel.shared

    var body: some Scene {
        Window("AsterLink", id: AppShell.mainWindowID) {
            MainWindowView(model: model)
        }
        .defaultSize(width: 960, height: 640)
        .windowResizability(.contentMinSize)
        .windowToolbarStyle(.unified(showsTitle: false))
        .restorationBehavior(.disabled)
        .commands {
            // One main window only: no "New Window".
            CommandGroup(replacing: .newItem) {}
            CommandGroup(replacing: .appSettings) {
                Button("设置…") { model.shell.showMainWindow(model.session?.phase == .signedIn ? .account : nil) }
                    .keyboardShortcut(",")
            }
        }

        MenuBarExtra {
            MenuBarPanel(model: model)
        } label: {
            MenuBarIcon(model: model)
        }
        .menuBarExtraStyle(.window)
    }
}

/// Owns everything that must outlive the main window (handoff 5.6): the start-up, the background
/// service watch, the wake refresh, the Dock policy and the exit flow.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var model: AppModel { AppModel.shared }
    private var observers: [NSObjectProtocol] = []

    func applicationDidFinishLaunching(_ notification: Notification) {
        AppearancePreference.current.apply()
        let center = NotificationCenter.default
        observers.append(center.addObserver(forName: NSWindow.willCloseNotification, object: nil, queue: .main) { note in
            MainActor.assumeIsolated {
                guard let window = note.object as? NSWindow, AppShell.isMainWindow(window) else { return }
                AppModel.shared.shell.mainWindowClosed()
            }
        })
        guard let session = model.session else { return }
        let helper = model.helper
        // Started once for the whole run; launch() itself ignores a second call.
        Task { await session.launch() }
        Task { await helper.watchRegistration() }
        Self.watchApproval()
        // The main window becoming key replaces "the app became active": opening the menu bar
        // panel activates the app too, and must not cost an extra sync.
        observers.append(center.addObserver(forName: NSWindow.didBecomeKeyNotification, object: nil, queue: .main) { note in
            MainActor.assumeIsolated {
                guard let window = note.object as? NSWindow, AppShell.isMainWindow(window) else { return }
                helper.refreshRegistration()
                Task { await session.refresh() }
            }
        })
        observers.append(NSWorkspace.shared.notificationCenter.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) { _ in
            // After sleep nothing cached is trusted: re-sync catalog and authorization at once.
            MainActor.assumeIsolated { _ = Task { await session.refresh() } }
        })
    }

    /// Approved in System Settings: continue the start-up that failed without it.
    private static func watchApproval() {
        let helper = AppModel.shared.helper
        withObservationTracking { _ = helper.registration } onChange: {
            Task { @MainActor in
                if helper.registration == .enabled, let session = AppModel.shared.session {
                    Task { await session.serviceBecameAvailable() }
                }
                Self.watchApproval()
            }
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    /// Opening the app again (Finder, Launchpad, Spotlight) while it lives in the menu bar.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        model.shell.showMainWindow()
        return false
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let session = model.session else { return .terminateNow }
        Task {
            await session.shutdown()
            NSApp.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }
}

/// The window side of the shell: which page shows, the line search, and bringing the one main
/// window back from the menu bar (handoff 5.5).
@MainActor
@Observable
final class AppShell {
    enum Page: Hashable { case connect, lines, account }

    static let mainWindowID = "main"
    var page: Page = .connect
    var lineQuery = ""
    var lineFilter: LineFilter = .all
    /// Captured from a live SwiftUI view: the only way to reopen a closed `Window` scene.
    @ObservationIgnored var openWindow: OpenWindowAction?

    static func isMainWindow(_ window: NSWindow) -> Bool {
        window.identifier?.rawValue.hasPrefix(mainWindowID) == true
    }

    /// Back to a normal app (Dock icon, app menu, ⌘Tab), then the window in front with focus.
    func showMainWindow(_ page: Page? = nil) {
        if let page { self.page = page }
        NSApp.setActivationPolicy(.regular)
        // The activation must come after the policy change has taken effect (next run-loop turn).
        DispatchQueue.main.async { [self] in
            if let openWindow {
                openWindow(id: Self.mainWindowID)
            } else if let window = NSApp.windows.first(where: Self.isMainWindow) {
                window.makeKeyAndOrderFront(nil)
            }
            NSApp.activate()
        }
    }

    /// Red button or ⌘W: keep running in the menu bar only; the connection is untouched.
    func mainWindowClosed() {
        DispatchQueue.main.async {
            guard !NSApp.windows.contains(where: { Self.isMainWindow($0) && $0.isVisible }) else { return }
            NSApp.setActivationPolicy(.accessory)
            CloseHint.showOnce()
        }
    }
}

/// The brand shield as a template image (the system tints it for light, dark and highlighted menu bars):
/// outline when not connected, solid when connected, with an exclamation mark when something is wrong.
struct MenuBarIcon: View {
    let model: AppModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Image(nsImage: Self.image(named: name))
            .onAppear { model.shell.openWindow = openWindow }
            .accessibilityLabel("AsterLink")
    }

    private var name: String {
        switch model.session?.snapshot.state {
        case .connected: "MenuBarActiveTemplate"
        case .error, .blocked: "MenuBarErrorTemplate"
        default: "MenuBarIdleTemplate"
        }
    }

    private static func image(named name: String) -> NSImage {
        // Not bundled when run straight from SwiftPM (development): fall back to a plain symbol.
        guard let image = NSImage(named: name) else {
            return NSImage(systemSymbolName: "shield", accessibilityDescription: "AsterLink") ?? NSImage()
        }
        image.isTemplate = true
        image.size = NSSize(width: 18, height: 18)
        return image
    }
}

/// Wires the real collaborators once. A missing or invalid build configuration yields no
/// session at all: there is no default server to fall back to.
@MainActor
struct AppModel {
    let session: SessionController?
    let helper: HelperClient
    let diagnostics: AppDiagnostics
    let shell: AppShell

    static let shared = AppModel.make()

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
        guard let environment else { return AppModel(session: nil, helper: helper, diagnostics: diagnostics, shell: AppShell()) }

        var transport: CoreTransport = XPCCoreTransport(connection: connection, appVersion: diagnostics.appVersion)
        #if DEBUG
        if channel == .development, let path = ProcessInfo.processInfo.environment["ASTERLINK_DEV_CORE"] {
            transport = DirectCoreTransport(executable: URL(fileURLWithPath: path))
        }
        #endif
        let client = CoreClient(transport: transport, environment: environment, appVersion: diagnostics.appVersion)
        let session = SessionController(client: client, environment: environment, credentials: KeychainCredentialStore(),
                                        preferences: UserDefaultsLoginPreferences(), devices: KeychainDeviceIdentity())
        return AppModel(session: session, helper: helper, diagnostics: diagnostics, shell: AppShell())
    }
}

import AsterLinkKit
import Observation
import SwiftUI

enum Appearance: String, CaseIterable, Identifiable {
    case system, light, dark

    var id: String { rawValue }
    var title: String { self == .system ? "跟随系统" : self == .light ? "浅色" : "深色" }
    var scheme: ColorScheme? { self == .system ? nil : self == .light ? .light : .dark }
}

/// What a fixture (a prepared screen for the screenshot suite) asks the interface to show first.
struct LaunchScreen: Equatable {
    var tab: AppTab = .connect
    var linesQuery = ""
}

enum AppTab: String, Hashable { case connect, lines, account }

/// The root object: configuration, the session controller and the few app-wide choices.
@MainActor
@Observable
final class AppModel {
    let config: Result<AppConfig, ClientEnvironmentError>
    let session: SessionController?
    var launchScreen = LaunchScreen()
    var tab: AppTab = .connect
    /// The website page open in the in-app browser, if any.
    var web: WebPage?
    @ObservationIgnored private var launched = false
    /// A prepared screen (UI fixtures) has no Core behind it, so nothing may talk to one.
    private(set) var fixtureMode = false

    var appearance: Appearance {
        didSet { UserDefaults.standard.set(appearance.rawValue, forKey: "appearance") }
    }

    init() {
        appearance = Appearance(rawValue: UserDefaults.standard.string(forKey: "appearance") ?? "") ?? .system
        config = AppConfig.load()
        if case .success(let config) = self.config {
            // Requests go to the app's own Core until a VPN runs, then to the extension's (TunnelBridge).
            let bridge = TunnelBridge(local: MobileTransport())
            let client = CoreClient(transport: bridge)
            session = SessionController(client: client, core: AppCoreHost(), target: config.target, credentials: KeychainCredentialStore(),
                                        preferences: DefaultsPreferences(), devices: KeychainDeviceID(),
                                        tunnel: NetworkTunnelControl(controller: TunnelController(), bridge: bridge), alerts: NotificationAlerts())
        } else {
            session = nil
        }
        #if DEBUG
        // The screenshot suite pins the appearance through the app's own preference (the same one the
        // 我的 page sets), because the simulator's system appearance did not reach the app in testing.
        if let forced = Appearance(rawValue: ProcessInfo.processInfo.environment["ASTER_APPEARANCE"] ?? "") { appearance = forced }
        if let name = ProcessInfo.processInfo.environment["ASTER_UI_FIXTURE"], let session {
            let fixture = UIFixtures.make(name)
            session.showFixture(fixture.apply)
            launchScreen = fixture.screen
            tab = fixture.screen.tab
            launched = true
            fixtureMode = true
        }
        #endif
    }

    var websiteBase: URL? { try? config.get().environment.websiteBase }

    /// Starts the Core and signs in with a remembered password, once. The unit-test host runs
    /// the app too and drives the Core itself, so it stays out of the way.
    func launchIfNeeded() async {
        guard !launched, let session else { return }
        launched = true
        if ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil { return }
        await session.launch()
    }

    /// The app came to the front or went away; the tunnel extension polls the catalog only while it is seen.
    func sceneChanged(active: Bool) {
        guard !fixtureMode, let session else { return }
        Task {
            await session.setForeground(active)
            if active { await session.onForeground() }
        }
    }

    func openWebsite(_ path: String, title: String) {
        guard let base = websiteBase else { return }
        web = WebPage(url: base.appendingPathComponent(path), title: title)
    }
}

struct WebPage: Identifiable, Equatable {
    let url: URL
    let title: String
    var id: URL { url }
}

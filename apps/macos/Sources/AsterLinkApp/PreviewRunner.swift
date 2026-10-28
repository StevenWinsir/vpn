#if DEBUG
import AppKit
import AsterLinkShared
import SwiftUI

/// DEBUG-only design preview (handoff_app_standardization.md 5.10), the macOS counterpart of the
/// Windows PreviewRunner: `AsterLink --preview <dir>` drives a real SessionController over a scripted
/// Core into each scenario, hosts the real views in an off-screen window, and writes
///   <dir>/<scenario>.png   the window as drawn by AppKit (title bar included)
///   <dir>/texts.json       every string the accessibility tree exposes, per scenario
///   <dir>/manifest.json    scenario, size, appearance, file
/// scripts/test-native-macos.py --suite ui checks the set is complete and runs the copy guard over the
/// texts. Release builds do not contain this file.
@MainActor
enum PreviewRunner {
    static func run(output: URL) {
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        Task { @MainActor in
            do {
                try await renderAll(to: output)
                exit(0)
            } catch {
                FileHandle.standardError.write(Data("preview failed: \(error)\n".utf8))
                exit(1)
            }
        }
        app.run()
    }

    // MARK: scenarios

    enum Surface { case window(AppShell.Page?), menuBar }

    struct Scenario {
        let name: String
        let surface: Surface
        var dark = false
        var size = CGSize(width: 960, height: 640)
        var registration: HelperRegistration = .enabled
        var signedIn = true
        var snapshot: [String: Any] = Fixture.ready()
        var failReadsWith: String?
        var query = ""
    }

    static var scenarios: [Scenario] {
        let minimum = CGSize(width: 820, height: 560)
        return [
            Scenario(name: "login-light", surface: .window(nil), signedIn: false),
            Scenario(name: "login-dark", surface: .window(nil), dark: true, signedIn: false),
            Scenario(name: "login-service-setup", surface: .window(nil), registration: .notRegistered, signedIn: false),
            Scenario(name: "connect-ready", surface: .window(.connect)),
            Scenario(name: "connect-no-line", surface: .window(.connect), snapshot: Fixture.ready(selected: "")),
            Scenario(name: "connect-connected-light", surface: .window(.connect), snapshot: Fixture.connected()),
            Scenario(name: "connect-connected-dark", surface: .window(.connect), dark: true, snapshot: Fixture.connected()),
            Scenario(name: "connect-no-plan", surface: .window(.connect), snapshot: Fixture.refused("upgrade_required", "当前账号没有有效套餐，请前往网站购买")),
            Scenario(name: "connect-plan-expired", surface: .window(.connect), snapshot: Fixture.refused("subscription_expired", "套餐已到期，请前往网站续费")),
            Scenario(name: "connect-quota-exhausted", surface: .window(.connect), snapshot: Fixture.refused("quota_exhausted", "套餐流量已用完，请前往网站续费或升级")),
            Scenario(name: "connect-failed", surface: .window(.connect), snapshot: Fixture.failed()),
            Scenario(name: "connect-minimum-size", surface: .window(.connect), size: minimum, snapshot: Fixture.connected()),
            Scenario(name: "lines-light", surface: .window(.lines)),
            Scenario(name: "lines-dark", surface: .window(.lines), dark: true),
            Scenario(name: "lines-empty", surface: .window(.lines), snapshot: Fixture.ready(nodes: [])),
            Scenario(name: "lines-no-match", surface: .window(.lines), query: "没有这条线路"),
            Scenario(name: "lines-connected", surface: .window(.lines), snapshot: Fixture.connected()),
            Scenario(name: "lines-minimum-size", surface: .window(.lines), size: minimum),
            Scenario(name: "account-light", surface: .window(.account)),
            Scenario(name: "account-dark", surface: .window(.account), dark: true),
            Scenario(name: "account-service-problem", surface: .window(.account),
                     failReadsWith: "后台服务需要更新，请在“账户”页重新安装后台服务"),
            Scenario(name: "account-service-not-allowed", surface: .window(.account), registration: .requiresApproval),
            Scenario(name: "menubar-signed-out-light", surface: .menuBar, signedIn: false),
            Scenario(name: "menubar-signed-out-dark", surface: .menuBar, dark: true, signedIn: false),
            Scenario(name: "menubar-ready-light", surface: .menuBar),
            Scenario(name: "menubar-ready-dark", surface: .menuBar, dark: true),
            Scenario(name: "menubar-connected-light", surface: .menuBar, snapshot: Fixture.connected()),
            Scenario(name: "menubar-connected-dark", surface: .menuBar, dark: true, snapshot: Fixture.connected()),
        ]
    }

    // MARK: rendering

    private static func renderAll(to output: URL) async throws {
        try FileManager.default.createDirectory(at: output, withIntermediateDirectories: true)
        var texts: [String: [String]] = [:]
        var manifest: [[String: Any]] = []
        for scenario in scenarios {
            let (image, strings, size) = try await render(scenario)
            let file = scenario.name + ".png"
            try image.write(to: output.appendingPathComponent(file))
            texts[scenario.name] = strings
            manifest.append(["scenario": scenario.name, "file": file, "dark": scenario.dark,
                             "width": Int(size.width), "height": Int(size.height)])
            print("rendered \(scenario.name) (\(strings.count) strings)")
        }
        try JSONSerialization.data(withJSONObject: texts, options: [.prettyPrinted, .sortedKeys])
            .write(to: output.appendingPathComponent("texts.json"))
        try JSONSerialization.data(withJSONObject: ["scenarios": manifest], options: [.prettyPrinted, .sortedKeys])
            .write(to: output.appendingPathComponent("manifest.json"))
    }

    private static func render(_ scenario: Scenario) async throws -> (Data, [String], CGSize) {
        let transport = ScriptedTransport(snapshot: scenario.snapshot, failReadsWith: scenario.failReadsWith)
        let environment = try ClientEnvironment.parse("APP_ENV=release\nAPI_BASE_URL=https://api.example.com/api/v1/client\nWEBSITE_BASE_URL=https://www.example.com\n")
        let store = InMemoryCredentialStore()
        let preferences = PreviewPreferences()
        let email = "user@example.com"
        if scenario.signedIn {
            try store.save(account: CredentialAccount.key(apiBase: environment.apiBase, email: email), password: "preview")
            preferences.rememberedEmail = email
        }
        let client = CoreClient(transport: transport, environment: environment, appVersion: "1.0.0")
        let session = SessionController(client: client, environment: environment, credentials: store, preferences: preferences,
                                        devices: FixedDeviceIdentity("33333333-3333-4333-8333-333333333333"),
                                        pollInterval: .milliseconds(100))
        if !scenario.signedIn { session.email = email }
        let policy = try SigningPolicy(channel: .development, teamID: nil)
        let registration = scenario.registration
        let helper = HelperClient(connection: HelperConnection(policy: policy), status: { registration }, installProblem: nil)
        let diagnostics = AppDiagnostics(appVersion: "1.0.0", isDevelopment: false)
        let shell = AppShell()
        shell.lineQuery = scenario.query
        await session.launch()
        if scenario.failReadsWith != nil { try await Task.sleep(for: .milliseconds(400)) }

        let model = AppModel(session: session, helper: helper, diagnostics: diagnostics, shell: shell)
        let window: NSWindow
        switch scenario.surface {
        case let .window(page):
            if let page { shell.page = page }
            window = NSWindow(contentRect: NSRect(origin: .zero, size: scenario.size),
                              styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                              backing: .buffered, defer: false)
            window.titleVisibility = .hidden
            window.titlebarAppearsTransparent = true
            window.contentView = NSHostingView(rootView: MainWindowView(model: model))
        case .menuBar:
            let host = NSHostingView(rootView: MenuBarPanel(model: model))
            window = NSWindow(contentRect: NSRect(origin: .zero, size: host.fittingSize), styleMask: [.borderless],
                              backing: .buffered, defer: false)
            window.contentView = host
        }
        window.appearance = NSAppearance(named: scenario.dark ? .darkAqua : .aqua)
        window.isReleasedWhenClosed = false
        window.setFrameOrigin(NSPoint(x: -20_000, y: -20_000))
        window.orderFrontRegardless()
        if case .menuBar = scenario.surface, let host = window.contentView {
            try await Task.sleep(for: .milliseconds(200))
            window.setContentSize(host.fittingSize)
        }
        // Let SwiftUI lay out, load fonts and settle animations to their end state.
        try await Task.sleep(for: .milliseconds(900))

        guard let frame = window.contentView?.superview ?? window.contentView else { throw PreviewError("no content view") }
        frame.layoutSubtreeIfNeeded()
        guard let rep = frame.bitmapImageRepForCachingDisplay(in: frame.bounds) else { throw PreviewError("no bitmap") }
        frame.cacheDisplay(in: frame.bounds, to: rep)
        guard let png = rep.representation(using: .png, properties: [:]) else { throw PreviewError("png") }
        let strings = await visibleTexts()
        let size = window.frame.size
        session.stopForPreview()
        window.orderOut(nil)
        return (png, strings, size)
    }

    /// Every title, value, description, placeholder and help text of the app's accessibility tree (only
    /// the scenario's window is on screen). Read through the system accessibility API from a background
    /// thread, as VoiceOver would; SwiftUI does not build the tree for in-process AppKit queries.
    private static func visibleTexts() async -> [String] {
        await Task.detached { () -> [String] in
            var found: [String] = []
            func walk(_ element: AXUIElement, _ depth: Int) {
                guard depth < 60 else { return }
                for attribute in [kAXTitleAttribute, kAXValueAttribute, kAXDescriptionAttribute, kAXPlaceholderValueAttribute, kAXHelpAttribute] {
                    var value: CFTypeRef?
                    if AXUIElementCopyAttributeValue(element, attribute as CFString, &value) == .success, let text = value as? String,
                       !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, !found.contains(text) {
                        found.append(text)
                    }
                }
                var children: CFTypeRef?
                if AXUIElementCopyAttributeValue(element, kAXChildrenAttribute as CFString, &children) == .success,
                   let list = children as? [AXUIElement] {
                    for child in list { walk(child, depth + 1) }
                }
            }
            walk(AXUIElementCreateApplication(getpid()), 0)
            return found
        }.value
    }
}

struct PreviewError: Error, CustomStringConvertible {
    let description: String
    init(_ description: String) { self.description = description }
}

private final class PreviewPreferences: LoginPreferences, @unchecked Sendable {
    var rememberedEmail: String?
}

private extension SessionController {
    func stopForPreview() { Task { await shutdown() } }
}

/// A Core that answers every command with one fixed snapshot (and, optionally, fails reads the way an
/// unreachable background service does).
private final class ScriptedTransport: CoreTransport, @unchecked Sendable {
    private let snapshot: [String: Any]
    private let failReadsWith: String?
    private let lock = NSLock()
    private var signedIn = false

    init(snapshot: [String: Any], failReadsWith: String?) {
        self.snapshot = snapshot
        self.failReadsWith = failReadsWith
    }

    func start() async throws {}

    private func answer(_ command: String) -> [String: Any] {
        lock.lock(); defer { lock.unlock() }
        if command == "login" { signedIn = true }
        return signedIn ? snapshot : Fixture.signedOut()
    }
    func stop() async {}

    func send(command: String, generation: UInt64, payload: Data) async throws -> Data {
        if command == "read_snapshot", let failReadsWith { throw HelperUnavailable(failReadsWith) }
        var result = answer(command)
        if command != "read_snapshot" && command != "disconnect" && command != "logout" { result["action_error"] = NSNull() }
        return try JSONSerialization.data(withJSONObject: ["request_id": "preview", "protocol_version": AsterLinkIdentity.protocolVersion,
                                                           "generation": 1, "ok": true, "result": result])
    }
}

/// Snapshot documents in the Core's wire format (shared/contracts/snapshot-v1.json).
enum Fixture {
    static let nodes: [[String: Any]] = [
        node("hk-1", "香港专线 01", "香港", "dedicated", 1500),
        node("hk-2", "香港专线 02", "香港", "dedicated", 1500),
        node("hk-3", "香港 03", "香港", "direct", 1000),
        node("jp-1", "东京专线 01", "日本", "dedicated", 2000),
        node("jp-2", "大阪 01", "日本", "direct", 1000),
        node("us-1", "洛杉矶 01", "美国", "direct", 500),
        node("sg-1", "新加坡专线 01", "新加坡", "dedicated", 1500),
    ]

    static func node(_ id: String, _ name: String, _ region: String, _ type: String, _ rate: Int) -> [String: Any] {
        ["id": id, "name": name, "region": region, "line_type": type, "rate_permille": rate]
    }

    static func entitlement(authorized: Bool = true, reason: String = "", message: String = "", remaining: Int64 = 61_203_283_968) -> [String: Any] {
        let expires = ISO8601DateFormatter().string(from: Date().addingTimeInterval(23 * 86_400))
        return ["plan_name": authorized ? "专线月付" : "", "subscription_expires_at": authorized ? expires : NSNull(),
                "authorization_expires_at": NSNull(), "lease_remaining_seconds": 0, "account_authorized": authorized,
                "can_connect": authorized, "reason": reason, "reason_message": message,
                "total_bytes": authorized ? Int64(107_374_182_400) : 0, "remaining_bytes": authorized ? remaining : 0, "used_units": 0]
    }

    static func base(state: String, nodes: [[String: Any]] = nodes, selected: String = "hk-1",
                     entitlement: [String: Any] = entitlement(), metering: Any = NSNull(), error: Any = NSNull()) -> [String: Any] {
        ["generation": 1, "state": state,
         "user": ["id": "u1", "email": "user@example.com", "name": "", "role": "user"],
         "entitlement": entitlement, "nodes": nodes, "selected_node_id": selected,
         "catalog_revision": "r1", "profile_version": "p1", "config_ready": !selected.isEmpty,
         "tunnel_health": state == "connected" ? "up" : "idle", "metering": metering, "last_error": error, "core_version": "1.0.0"]
    }

    static func ready(selected: String = "hk-1", nodes: [[String: Any]] = nodes) -> [String: Any] {
        base(state: "ready", nodes: nodes, selected: nodes.isEmpty ? "" : selected)
    }

    static func connected() -> [String: Any] {
        let history = (0..<60).map { i -> [String: Any] in
            let wave = sin(Double(i) / 6) * 0.5 + 0.5
            return ["upload": Int64(40_000 + wave * 90_000), "download": Int64(600_000 + wave * 2_400_000 + Double(i % 7) * 120_000)]
        }
        let metering: [String: Any] = [
            "source": "client_reported", "upload_bytes": 52_428_800, "download_bytes": 1_288_490_188, "last_sequence": 12,
            "upload_bytes_per_second": 98_304, "download_bytes_per_second": 2_936_012,
            "confirmed_upload_bytes": 41_943_040, "confirmed_download_bytes": 1_181_116_006,
            "confirmed_remaining_bytes": 61_203_283_968, "estimated_remaining_bytes": 61_029_632_000,
            "rate_permille": 1500, "pending": true, "history": history,
        ]
        return base(state: "connected", metering: metering)
    }

    static func refused(_ reason: String, _ message: String) -> [String: Any] {
        base(state: "blocked", nodes: [], selected: "", entitlement: entitlement(authorized: false, reason: reason, message: message))
    }

    static func failed() -> [String: Any] {
        base(state: "error", error: ["code": "tunnel_failed", "message": "连接失败，请更换线路或稍后重试，或联系客服"])
    }

    static func signedOut() -> [String: Any] {
        ["generation": 0, "state": "signed_out", "user": NSNull(), "entitlement": NSNull(), "nodes": [], "selected_node_id": "",
         "catalog_revision": "", "profile_version": "", "config_ready": false, "tunnel_health": "idle", "metering": NSNull(),
         "last_error": NSNull(), "core_version": "1.0.0"]
    }
}
#endif

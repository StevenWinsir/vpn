import Foundation
@testable import AsterLinkKit

/// A scripted Core: answers each ipc-v1 command from a handler, echoing request_id and generation
/// the way the real Core does.
final class ScriptedCore: CoreTransport, @unchecked Sendable {
    struct Command { let name: String; let generation: UInt64; let payload: [String: String] }
    enum Answer {
        case snapshot(CoreSnapshot, action: ErrorView? = nil)
        case refuse(code: String, message: String = "")
    }

    private let lock = NSLock()
    private var handler: (Command) -> Answer
    private(set) var log: [Command] = []

    init(_ handler: @escaping (Command) -> Answer) { self.handler = handler }

    func replace(_ handler: @escaping (Command) -> Answer) { lock.withLock { self.handler = handler } }
    func count(_ name: String) -> Int { lock.withLock { log.filter { $0.name == name }.count } }

    func call(_ request: String) -> String {
        let object = (try? JSONSerialization.jsonObject(with: Data(request.utf8))) as? [String: Any] ?? [:]
        let command = Command(name: object["command"] as? String ?? "", generation: (object["generation"] as? NSNumber)?.uint64Value ?? 0,
                              payload: object["payload"] as? [String: String] ?? [:])
        // The handler runs outside the lock so it may ask for counts itself.
        let current: (Command) -> Answer = lock.withLock { log.append(command); return handler }
        let answer = current(command)
        var response: [String: Any] = ["request_id": object["request_id"] ?? "", "protocol_version": 1, "generation": command.generation]
        switch answer {
        case .refuse(let code, let message):
            response["ok"] = false; response["error"] = ["code": code, "message": message]
        case .snapshot(let snapshot, let action):
            response["ok"] = true
            var result = (try? JSONSerialization.jsonObject(with: JSONEncoder().encode(snapshot))) as? [String: Any] ?? [:]
            if command.name == "hello" {
                result = ["component": "asterlink-core", "core_version": "9.9.9", "protocol_version": 1, "min_protocol_version": 1,
                          "max_protocol_version": 1, "mihomo_revision": "abcdef1234567890"]
            }
            if let action { result["action_error"] = ["code": action.code, "message": action.message] }
            response["result"] = result
        }
        return String(decoding: (try? JSONSerialization.data(withJSONObject: response)) ?? Data(), as: UTF8.self)
    }
}

actor MemoryCredentials: CredentialStore {
    var items: [String: String] = [:]
    var failSave = false
    func setFailSave(_ value: Bool) { failSave = value }
    func save(account: String, password: String) throws {
        if failSave { throw CredentialStoreError(status: -1) }
        items[account] = password
    }
    func read(account: String) -> String? { items[account] }
    func delete(account: String) { items[account] = nil }
}

final class MemoryPreferences: LoginPreferences, @unchecked Sendable {
    private let lock = NSLock()
    private var email: String?
    init(_ email: String? = nil) { self.email = email }
    func rememberedEmail() -> String? { lock.withLock { email } }
    func setRememberedEmail(_ email: String?) { lock.withLock { self.email = email } }
}

struct FixedDevice: DeviceIDProvider {
    func deviceID() async throws -> String { "33333333-3333-4333-8333-333333333333" }
}

struct NoopHost: CoreHost {
    func start() async throws {}
    func stop() async {}
}

final class RecordingAlerts: SessionAlerts, @unchecked Sendable {
    private let lock = NSLock()
    private var stopped: [Notice] = []
    var notices: [Notice] { lock.withLock { stopped } }
    func tunnelStopped(_ reason: Notice) { lock.withLock { stopped.append(reason) } }
    func clear() {}
}

let testTarget = LoginTarget(apiBase: "http://127.0.0.1:1/api/v1/client", channel: .development, appVersion: "0.1.0")

func vipSnapshot(generation: UInt64 = 1, state: String = "ready", selected: String = "", nodes: Bool = true) -> CoreSnapshot {
    CoreSnapshot(
        generation: generation, state: state, user: UserView(id: "u", email: "vip@example.invalid", name: "VIP", role: "user"),
        entitlement: EntitlementView(planName: "VIP", accountAuthorized: true, canConnect: true, totalBytes: 1 << 30, remainingBytes: 1 << 29),
        nodes: nodes ? [NodeView(id: "a", name: "HK 01", region: "HK", lineType: "dedicated", ratePermille: 1000),
                        NodeView(id: "b", name: "JP 01", region: "JP", lineType: "direct", ratePermille: 500)] : [],
        selectedNodeID: selected, catalogRevision: nodes ? "rev1" : "", configReady: !selected.isEmpty)
}

@MainActor
func makeController(core: ScriptedCore, credentials: MemoryCredentials = MemoryCredentials(), preferences: MemoryPreferences = MemoryPreferences(),
                    alerts: RecordingAlerts = RecordingAlerts(), clock: @escaping @Sendable () -> Int64 = { 0 }) -> SessionController {
    let client = CoreClient(transport: core)
    return SessionController(client: client, core: NoopHost(), target: testTarget, credentials: credentials, preferences: preferences,
                             devices: FixedDevice(), tunnel: DirectCoreTunnel(client: client), alerts: alerts, clock: clock,
                             idleInterval: .seconds(3600), liveInterval: .seconds(3600))
}

/// Answers the extension channel from a scripted Core, or fails like a stopped extension.
final class CoreChannel: ExtensionChannel, @unchecked Sendable {
    let core: ScriptedCore
    private let lock = NSLock()
    private var down = false
    init(_ core: ScriptedCore) { self.core = core }
    func setDown(_ value: Bool) { lock.withLock { down = value } }
    func send(_ request: String, completion: @escaping @Sendable (String?) -> Void) {
        completion(lock.withLock({ down }) ? nil : core.call(request))
    }
}

/// The app side of the handoff with two scripted Cores: the app's own and the extension's, joined
/// by a TunnelBridge exactly as in the app. Mirrors NetworkTunnelControl's contract.
final class FakeTunnel: TunnelControl, @unchecked Sendable {
    let bridge: TunnelBridge
    let channel: CoreChannel
    private let extensionCore: ScriptedCore
    private let lock = NSLock()
    private var isConfigured: Bool
    private(set) var connects = 0
    private(set) var stops = 0
    private var failure: (String, String)?

    init(app: ScriptedCore, tunnelCore: ScriptedCore, configured: Bool = true) {
        bridge = TunnelBridge(local: app, timeout: 5)
        channel = CoreChannel(tunnelCore)
        extensionCore = tunnelCore
        isConfigured = configured
    }

    var configured: Bool { get async { lock.withLock { isConfigured } } }
    func failNextConnect(code: String, message: String = "") { lock.withLock { failure = (code, message) } }

    func connect(generation: UInt64) async -> TunnelOutcome {
        lock.withLock { connects += 1; isConfigured = true }
        if let (code, message) = lock.withLock({ let value = failure; failure = nil; return value }) { return .refused(code: code, message: message) }
        bridge.useTunnel(channel)
        let client = CoreClient(transport: bridge)
        guard let snapshot = try? await client.readSnapshot() else { return .refused(code: LocalCodes.tunnelUnreachable) }
        return .replied(SnapshotReply(snapshot: snapshot))
    }

    func stop() async -> Bool {
        guard bridge.currentRoute == .tunnel else { return false }
        lock.withLock { stops += 1 }
        bridge.useApp()
        return true
    }
}

@MainActor
func makeTunnelController(app: ScriptedCore, tunnel: FakeTunnel, credentials: MemoryCredentials = MemoryCredentials(),
                          preferences: MemoryPreferences = MemoryPreferences(), alerts: RecordingAlerts = RecordingAlerts()) -> SessionController {
    SessionController(client: CoreClient(transport: tunnel.bridge), core: NoopHost(), target: testTarget, credentials: credentials,
                      preferences: preferences, devices: FixedDevice(), tunnel: tunnel, alerts: alerts, clock: { 0 },
                      idleInterval: .seconds(3600), liveInterval: .seconds(3600))
}

func connectedSnapshot(generation: UInt64, rate: Int64 = 1000, upload: Int64 = 0, download: Int64 = 0) -> CoreSnapshot {
    var snapshot = vipSnapshot(generation: generation, state: CoreSnapshot.State.connected, selected: "a")
    snapshot.metering = MeteringView(source: "client_reported", uploadBytes: upload, downloadBytes: download, ratePermille: rate)
    return snapshot
}

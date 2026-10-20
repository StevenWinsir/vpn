import Foundation
import Testing
@testable import AsterLinkKit

/// A Keychain whose reads can be held open, so a user action can be placed exactly while a read is in flight.
actor GatedCredentials: CredentialStore {
    var items: [String: String] = [:]
    private var gated = false
    private var waiting: [CheckedContinuation<Void, Never>] = []

    func gate() { gated = true }
    var pendingReads: Int { waiting.count }
    func release() {
        gated = false
        let all = waiting
        waiting = []
        all.forEach { $0.resume() }
    }

    func save(account: String, password: String) { items[account] = password }
    func read(account: String) async -> String? {
        if gated { await withCheckedContinuation { waiting.append($0) } }
        return items[account]
    }
    func delete(account: String) { items[account] = nil }
}

/// handoff_auth.md R4-03: the password was reset on the website while the old one was saved here.
@MainActor
struct PasswordResetTests {
    static let refused = ErrorView(code: "invalid_credentials", message: "邮箱或密码错误")
    static let revoked = CoreSnapshot(generation: 2, state: "signed_out", lastError: ErrorView(code: "native_session_expired", message: "登录已失效，请重新登录"))
    let email = "vip@example.invalid"
    var key: String { CredentialAccount.key(apiBase: testTarget.apiBase, email: email) }

    /// Logins succeed only with `accepted`; once `revokeOnRefresh` is set, refresh_catalog reports the session revoked.
    func server(accepted: String, revokeOnRefresh: Bool = false) -> ScriptedCore {
        ScriptedCore { command in
            switch command.name {
            case "login":
                command.payload["password"] == accepted ? .snapshot(vipSnapshot(generation: 1)) : .snapshot(.empty, action: Self.refused)
            case "logout": .snapshot(.empty)
            case "refresh_catalog" where revokeOnRefresh: .snapshot(Self.revoked)
            default: .snapshot(vipSnapshot(generation: max(command.generation, 1)))
            }
        }
    }

    func controller(_ core: ScriptedCore, _ credentials: any CredentialStore, _ preferences: MemoryPreferences = MemoryPreferences()) -> SessionController {
        let client = CoreClient(transport: core)
        return SessionController(client: client, core: NoopHost(), target: testTarget, credentials: credentials, preferences: preferences,
                                 devices: FixedDevice(), tunnel: DirectCoreTunnel(client: client), clock: { 0 },
                                 idleInterval: .seconds(3600), liveInterval: .seconds(3600))
    }

    func logins(_ core: ScriptedCore) -> [String?] { core.log.filter { $0.name == "login" }.map { $0.payload["email"] } }

    @Test func theOldSavedPasswordIsTriedOnceThenForgottenAndTheNewOneSaved() async {
        let credentials = MemoryCredentials()
        try? await credentials.save(account: key, password: "old-password")
        let preferences = MemoryPreferences(email)
        let core = server(accepted: "new-password")
        let controller = controller(core, credentials, preferences)
        await controller.launch()
        #expect(core.count("login") == 1)
        #expect(controller.state.phase == .signedOut && controller.state.message == .core("invalid_credentials", "邮箱或密码错误"))
        #expect(await credentials.read(account: key) == nil)
        #expect(preferences.rememberedEmail() == email && controller.state.email == email)
        try? await Task.sleep(for: .milliseconds(100))
        #expect(core.count("login") == 1, "no retry loop after a refused password")

        await controller.signIn(email: email, password: "new-password", remember: true)
        #expect(controller.state.phase == .signedIn)
        #expect(await credentials.read(account: key) == "new-password")
    }

    @Test func aTypoInAManualLoginKeepsTheSavedPassword() async {
        let credentials = MemoryCredentials()
        try? await credentials.save(account: key, password: "good-password")
        let controller = controller(server(accepted: "good-password"), credentials)
        await controller.signIn(email: email, password: "typo-password", remember: true)
        #expect(controller.state.phase == .signedOut)
        #expect(await credentials.read(account: key) == "good-password")
    }

    @Test func aManualLoginDuringTheLaunchKeychainReadWins() async {
        let credentials = GatedCredentials()
        await credentials.save(account: key, password: "old-password")
        await credentials.gate()
        let core = server(accepted: "new-password")
        let controller = controller(core, credentials, MemoryPreferences(email))
        let launch = Task { await controller.launch() }
        for _ in 0..<200 where await credentials.pendingReads == 0 { try? await Task.sleep(for: .milliseconds(5)) }
        // The login form is already up: the user types the new password before the Keychain answers.
        let typed = Task { await controller.signIn(email: email, password: "new-password", remember: true) }
        for _ in 0..<200 where core.count("login") == 0 { try? await Task.sleep(for: .milliseconds(5)) }
        await credentials.release()
        await typed.value
        await launch.value
        #expect(controller.state.phase == .signedIn)
        #expect(core.count("login") == 1, "the stale launch read must not log in again over the user's own login")
        #expect(await credentials.items[key] == "new-password")
    }

    @Test func aSilentReloginIsDroppedWhenTheUserSignsInToAnotherAccountFirst() async {
        let credentials = GatedCredentials()
        let core = server(accepted: "pw", revokeOnRefresh: true)
        let controller = controller(core, credentials)
        await controller.signIn(email: email, password: "pw", remember: true)
        await credentials.gate()
        await controller.refresh() // the server revoked the session: one silent re-login reads the Keychain
        #expect(controller.state.phase == .signedOut)
        for _ in 0..<200 where await credentials.pendingReads == 0 { try? await Task.sleep(for: .milliseconds(5)) }
        // Meanwhile the user signs in to another account.
        core.replace { command in command.name == "login" ? .snapshot(vipSnapshot(generation: 5)) : .snapshot(vipSnapshot(generation: 5)) }
        let other = Task { await controller.signIn(email: "other@example.invalid", password: "other-pw", remember: false) }
        for _ in 0..<200 where core.count("login") < 2 { try? await Task.sleep(for: .milliseconds(5)) }
        await credentials.release()
        await other.value
        try? await Task.sleep(for: .milliseconds(100))
        #expect(logins(core) == [email, "other@example.invalid"], "no third login with the expired account's saved password")
        #expect(controller.state.phase == .signedIn && controller.state.email == "other@example.invalid")
    }
}

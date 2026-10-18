import AsterCore
import AsterLinkKit
import Foundation
import Security

/// The public build parameters baked into Info.plist (handoff_app_ios.md 6.2), validated with
/// the same env-v1 rules as the build script. A Debug build may be pointed at an isolated local
/// backend with ASTER_API_BASE_OVERRIDE (the account acceptance suite); Release ignores it.
struct AppConfig: Sendable {
    let environment: ClientEnvironment
    let appVersion: String

    var target: LoginTarget {
        LoginTarget(apiBase: environment.apiBase.absoluteString, channel: environment.channel, appVersion: appVersion)
    }

    static func load(overrides: [String: String] = ProcessInfo.processInfo.environment) -> Result<AppConfig, ClientEnvironmentError> {
        let info = Bundle.main.infoDictionary ?? [:]
        var api = info["AsterAPIBaseURL"] as? String ?? ""
        #if DEBUG
        if let override = overrides["ASTER_API_BASE_OVERRIDE"], !override.isEmpty { api = override }
        #endif
        let text = "APP_ENV=\(info["AsterAppEnv"] as? String ?? "")\nAPI_BASE_URL=\(api)\nWEBSITE_BASE_URL=\(info["AsterWebsiteBaseURL"] as? String ?? "")\n"
        return Result { try ClientEnvironment.parse(text) }.mapError { $0 as? ClientEnvironmentError ?? .invalidEnvironment }
            .map { AppConfig(environment: $0, appVersion: info["CFBundleShortVersionString"] as? String ?? "0") }
    }
}

/// `MobileCall`: one ipc-v1 document in, one out (a blocking call into the in-process Core).
struct MobileTransport: CoreTransport {
    func call(_ request: String) -> String { CoreBridge.call(request) }
}

/// Starts and stops the in-process Core. `start` is idempotent: the unit-test host and the app
/// share one Go runtime, so a Core that is already running is reused, not an error.
struct AppCoreHost: CoreHost {
    func start() async throws {
        if isRunning() { return }
        try CoreLifecycle.start()
    }

    func stop() async { CoreLifecycle.stop() }

    private func isRunning() -> Bool {
        let probe = CoreRequest.command("read_snapshot", requestID: "probe")
        guard let response = try? JSONDecoder().decode(CoreResponse.self, from: Data(CoreBridge.call(probe).utf8)) else { return false }
        return response.error?.code != CoreBridge.notStartedCode
    }
}

/// The remembered password as a Keychain generic-password item (handoff_app_ios.md 6.3):
/// `service = <bundle>.credentials`, `account = SHA-256(API base + email)`, this device only,
/// never synchronised. A failure is reported, never downgraded to anything weaker.
struct KeychainCredentialStore: CredentialStore {
    let service: String

    init(service: String = (Bundle.main.bundleIdentifier ?? "com.asterlink.dev.asterlink") + ".credentials") { self.service = service }

    private func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
         kSecAttrAccount as String: account, kSecAttrSynchronizable as String: false]
    }

    func save(account: String, password: String) async throws {
        var update = query(account)
        let attributes: [String: Any] = [kSecValueData as String: Data(password.utf8),
                                         kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly]
        let status = SecItemUpdate(update as CFDictionary, attributes as CFDictionary)
        if status == errSecSuccess { return }
        guard status == errSecItemNotFound else { throw CredentialStoreError(status: status) }
        update.merge(attributes) { $1 }
        let added = SecItemAdd(update as CFDictionary, nil)
        guard added == errSecSuccess else { throw CredentialStoreError(status: added) }
    }

    func read(account: String) async throws -> String? {
        var request = query(account)
        request[kSecReturnData as String] = true
        request[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = item as? Data else { throw CredentialStoreError(status: status) }
        return String(data: data, encoding: .utf8)
    }

    func delete(account: String) async throws {
        let status = SecItemDelete(query(account) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw CredentialStoreError(status: status) }
    }
}

/// The remembered email and other non-secret choices live in UserDefaults; never a password or token.
final class DefaultsPreferences: LoginPreferences, @unchecked Sendable {
    private let defaults: UserDefaults
    private static let emailKey = "remembered_email"

    init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    func rememberedEmail() -> String? { defaults.string(forKey: Self.emailKey) }

    func setRememberedEmail(_ email: String?) {
        if let email { defaults.set(email, forKey: Self.emailKey) } else { defaults.removeObject(forKey: Self.emailKey) }
    }
}

/// The installation's device_id in the Keychain (this device only; survives reinstalls).
struct KeychainDeviceID: DeviceIDProvider {
    let service: String

    init(service: String = (Bundle.main.bundleIdentifier ?? "com.asterlink.dev.asterlink") + ".device") { self.service = service }

    func deviceID() async throws -> String { try DeviceIdentity.load(service: service) }
}

import Foundation
import Security

public enum CredentialError: Error, Equatable {
    case keychain(OSStatus)
    case invalidAccount
}

/// The remembered login. The password is held only by the Keychain; the account list is
/// separate and non-secret. There is intentionally no plaintext fallback anywhere.
public protocol CredentialStore: Sendable {
    func save(account: String, password: String) throws
    func password(for account: String) throws -> String?
    func delete(account: String) throws
}

/// Items are scoped to the exact API base plus the lower-cased email, so a changed
/// server address can never receive a password saved for another one.
public enum CredentialAccount {
    public static func key(apiBase: URL, email: String) -> String {
        "\(apiBase.absoluteString)|\(email.trimmingCharacters(in: .whitespaces).lowercased())"
    }
}

public final class KeychainCredentialStore: CredentialStore, @unchecked Sendable {
    private let service: String

    /// `service` is injectable so tests use an isolated namespace that never touches real items.
    public init(service: String = "com.asterlink.vpn.credentials") { self.service = service }

    private func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: account,
         kSecAttrSynchronizable as String: kCFBooleanFalse as Any]
    }

    public func save(account: String, password: String) throws {
        guard !account.isEmpty, !password.isEmpty else { throw CredentialError.invalidAccount }
        let data = Data(password.utf8)
        var attributes = query(account)
        attributes[kSecValueData as String] = data
        // Never leaves this Mac (no iCloud sync) and is unavailable while the Mac is locked.
        attributes[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        var status = SecItemAdd(attributes as CFDictionary, nil)
        if status == errSecDuplicateItem {
            status = SecItemUpdate(query(account) as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        }
        guard status == errSecSuccess else { throw CredentialError.keychain(status) }
    }

    public func password(for account: String) throws -> String? {
        var request = query(account)
        request[kSecReturnData as String] = true
        request[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = item as? Data else { throw CredentialError.keychain(status) }
        return String(data: data, encoding: .utf8)
    }

    public func delete(account: String) throws {
        let status = SecItemDelete(query(account) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw CredentialError.keychain(status) }
    }
}

/// Test double with the same contract, including injectable failure.
public final class InMemoryCredentialStore: CredentialStore, @unchecked Sendable {
    private let lock = NSLock()
    private var items: [String: String] = [:]
    public var failure: CredentialError?

    public init() {}

    public func save(account: String, password: String) throws {
        lock.lock(); defer { lock.unlock() }
        if let failure { throw failure }
        items[account] = password
    }

    public func password(for account: String) throws -> String? {
        lock.lock(); defer { lock.unlock() }
        if let failure { throw failure }
        return items[account]
    }

    public func delete(account: String) throws {
        lock.lock(); defer { lock.unlock() }
        if let failure { throw failure }
        items[account] = nil
    }

    public var accounts: [String] { lock.lock(); defer { lock.unlock() }; return Array(items.keys) }
}

/// Non-secret remembered choices (never the password): the last email and whether to remember.
public protocol LoginPreferences: Sendable {
    var rememberedEmail: String? { get set }
}

public struct UserDefaultsLoginPreferences: LoginPreferences, @unchecked Sendable {
    private let defaults: UserDefaults
    private static let key = "AsterLink.rememberedEmail"

    public init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    public var rememberedEmail: String? {
        get { defaults.string(forKey: Self.key) }
        nonmutating set {
            if let newValue { defaults.set(newValue, forKey: Self.key) } else { defaults.removeObject(forKey: Self.key) }
        }
    }
}

/// A fixed per-installation device UUID. It lives in the Keychain (this device only) rather
/// than a preference file, so clearing preferences cannot mint a fresh device to dodge the
/// plan's device limit.
public protocol DeviceIdentityProvider: Sendable {
    func deviceID() throws -> String
}

public final class KeychainDeviceIdentity: DeviceIdentityProvider, @unchecked Sendable {
    private let service: String
    private let account = "device-id"
    private let lock = NSLock()

    public init(service: String = "com.asterlink.vpn.device") { self.service = service }

    public func deviceID() throws -> String {
        lock.lock(); defer { lock.unlock() }
        let base: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                   kSecAttrAccount as String: account, kSecAttrSynchronizable as String: kCFBooleanFalse as Any]
        var read = base
        read[kSecReturnData as String] = true
        read[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(read as CFDictionary, &item)
        if status == errSecSuccess, let data = item as? Data, let text = String(data: data, encoding: .utf8), UUID(uuidString: text) != nil {
            return text.lowercased()
        }
        guard status == errSecItemNotFound || status == errSecSuccess else { throw CredentialError.keychain(status) }
        let fresh = UUID().uuidString.lowercased()
        var add = base
        add[kSecValueData as String] = Data(fresh.utf8)
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let addStatus = SecItemAdd(add as CFDictionary, nil)
        guard addStatus == errSecSuccess else { throw CredentialError.keychain(addStatus) }
        return fresh
    }
}

public struct FixedDeviceIdentity: DeviceIdentityProvider {
    public let id: String
    public init(_ id: String) { self.id = id }
    public func deviceID() throws -> String { id }
}

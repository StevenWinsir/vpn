import Foundation
import Security

/// The installation's device_id (handoff_app_ios.md 6.3): a random UUID created once and kept
/// in the Keychain (this device only, never synchronised). It survives app deletion on iOS, so
/// a reinstall does not count as a new device against the plan's device limit. No IDFV, IDFA
/// or hardware identifier is read.
public enum DeviceIdentity {
    public enum Failure: Error, Equatable {
        case keychain(OSStatus)
    }

    public static func load(service: String) throws -> String {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: "device_id",
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecSuccess, let data = item as? Data, let text = String(data: data, encoding: .utf8), UUID(uuidString: text) != nil {
            return text.lowercased()
        }
        guard status == errSecItemNotFound || status == errSecSuccess else { throw Failure.keychain(status) }
        let fresh = UUID().uuidString.lowercased()
        let add: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: "device_id",
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            kSecAttrSynchronizable as String: false,
            kSecValueData as String: Data(fresh.utf8),
        ]
        SecItemDelete(query.filter { $0.key != kSecReturnData as String && $0.key != kSecMatchLimit as String } as CFDictionary)
        let added = SecItemAdd(add as CFDictionary, nil)
        guard added == errSecSuccess else { throw Failure.keychain(added) }
        return fresh
    }
}

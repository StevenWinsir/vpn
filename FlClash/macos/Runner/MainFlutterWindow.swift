import Cocoa
import FlutterMacOS
import window_manager
import LaunchAtLogin
import Security

class MainFlutterWindow: NSWindow {
    override func awakeFromNib() {
        let flutterViewController = FlutterViewController()
        let windowFrame = self.frame
        self.contentViewController = flutterViewController
        self.setFrame(windowFrame, display: true)
        
        FlutterMethodChannel(
            name: "launch_at_startup", binaryMessenger: flutterViewController.engine.binaryMessenger
        )
        .setMethodCallHandler { (_ call: FlutterMethodCall, result: @escaping FlutterResult) in
            switch call.method {
            case "launchAtStartupIsEnabled":
                result(LaunchAtLogin.isEnabled)
            case "launchAtStartupSetEnabled":
                if let arguments = call.arguments as? [String: Any] {
                    LaunchAtLogin.isEnabled = arguments["setEnabledValue"] as! Bool
                }
                result(nil)
            default:
                result(FlutterMethodNotImplemented)
            }
        }
        
        FlutterMethodChannel(
            name: "asterlink/managed_credentials",
            binaryMessenger: flutterViewController.engine.binaryMessenger
        ).setMethodCallHandler { call, result in
            DispatchQueue.global(qos: .userInitiated).async {
                let response = ManagedLoginKeychain.handle(call)
                DispatchQueue.main.async { result(response) }
            }
        }

        RegisterGeneratedPlugins(registry: flutterViewController)
        super.awakeFromNib()
    }
    override public func order(_ place: NSWindow.OrderingMode, relativeTo otherWin: Int) {
        super.order(place, relativeTo: otherWin)
        hiddenWindowAtLaunch()
    }
}

private enum ManagedLoginKeychain {
    static let service = (Bundle.main.bundleIdentifier ?? "com.follow.clash") + ".managed-login"

    static func failure() -> FlutterError {
        FlutterError(code: "credential_store_unavailable", message: "macOS Keychain unavailable", details: nil)
    }

    static func validScope(_ value: String) -> Bool {
        guard value.utf8.count <= 2048, let url = URLComponents(string: value),
              let host = url.host, !host.isEmpty, url.user == nil, url.password == nil,
              url.query == nil, url.fragment == nil, url.path == "/api/v1/client",
              url.port == nil || (1...65535).contains(url.port!) else { return false }
        return url.scheme == "https" || (url.scheme == "http" && ["127.0.0.1", "::1", "[::1]"].contains(host))
    }

    static func validLogin(_ input: [String: Any]) -> Bool {
        guard let email = input["email"] as? String, let password = input["password"] as? String else { return false }
        return !email.isEmpty && email.contains("@") && email.utf8.count <= 254 &&
            email == email.trimmingCharacters(in: .whitespacesAndNewlines) &&
            !password.isEmpty && password.utf8.count <= 72
    }

    static func handle(_ call: FlutterMethodCall) -> Any? {
        guard let input = call.arguments as? [String: Any],
              let scope = input["scope"] as? String, validScope(scope) else { return failure() }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: scope,
            kSecAttrSynchronizable as String: false,
            kSecUseAuthenticationUI as String: kSecUseAuthenticationUIFail,
        ]
        switch call.method {
        case "read":
            var lookup = query
            lookup[kSecReturnData as String] = true
            lookup[kSecMatchLimit as String] = kSecMatchLimitOne
            var item: CFTypeRef?
            let status = SecItemCopyMatching(lookup as CFDictionary, &item)
            if status == errSecItemNotFound { return nil }
            guard status == errSecSuccess, let data = item as? Data, data.count <= 4096,
                  let login = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  login.count == 2, validLogin(login) else { return failure() }
            return login
        case "save":
            guard validLogin(input), let data = try? JSONSerialization.data(withJSONObject: [
                "email": input["email"]!, "password": input["password"]!,
            ]) else { return failure() }
            var status = SecItemUpdate(query as CFDictionary, [kSecValueData as String: data] as CFDictionary)
            if status == errSecItemNotFound {
                var entry = query
                entry[kSecValueData as String] = data
                entry[kSecAttrLabel as String] = "AsterLink remembered sign-in"
                status = SecItemAdd(entry as CFDictionary, nil)
            }
            return status == errSecSuccess ? true : failure()
        case "delete":
            let status = SecItemDelete(query as CFDictionary)
            return status == errSecSuccess || status == errSecItemNotFound ? true : failure()
        default:
            return FlutterMethodNotImplemented
        }
    }
}

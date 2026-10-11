import Foundation

public enum ClientEnvironmentError: Error, Equatable {
    case malformedLine(Int)
    case unknownKey(String)
    case duplicateKey(String)
    case missingKey(String)
    case unsafeValue(String)
    case invalidEnvironment
    case invalidURL(String)
}

/// Public build parameters only (handoff_app.md section 4.2). Parsing a `.env` is not
/// `source .env`: no interpolation, quoting, `export`, duplicates or unknown keys.
/// scripts/native_env.py implements the same rules; shared/contracts/env-v1.json
/// pins both to identical accept/reject behaviour.
public struct ClientEnvironment: Equatable, Sendable {
    public let channel: BuildChannel
    public let apiBase: URL
    public let websiteBase: URL

    public static let allowedKeys: Set<String> = ["APP_ENV", "API_BASE_URL", "WEBSITE_BASE_URL"]
    public static let apiPath = "/api/v1/client"

    public init(channel: BuildChannel, apiBase: URL, websiteBase: URL) {
        self.channel = channel
        self.apiBase = apiBase
        self.websiteBase = websiteBase
    }

    public static func parse(_ text: String) throws -> ClientEnvironment {
        var values: [String: String] = [:]
        for (index, rawLine) in text.split(separator: "\n", omittingEmptySubsequences: false).enumerated() {
            let line = rawLine.trimmingCharacters(in: CharacterSet(charactersIn: "\r"))
            if line.isEmpty || line.hasPrefix("#") { continue }
            guard let equals = line.firstIndex(of: "=") else { throw ClientEnvironmentError.malformedLine(index + 1) }
            let key = String(line[line.startIndex..<equals])
            let value = String(line[line.index(after: equals)...])
            guard allowedKeys.contains(key) else { throw ClientEnvironmentError.unknownKey(key) }
            guard values[key] == nil else { throw ClientEnvironmentError.duplicateKey(key) }
            guard !value.isEmpty, value.allSatisfy({ isSafe($0) }) else { throw ClientEnvironmentError.unsafeValue(key) }
            values[key] = value
        }
        for key in allowedKeys.sorted() where values[key] == nil { throw ClientEnvironmentError.missingKey(key) }
        guard let channel = BuildChannel(rawValue: values["APP_ENV"]!) else { throw ClientEnvironmentError.invalidEnvironment }
        let api = try validatedURL(values["API_BASE_URL"]!, key: "API_BASE_URL", channel: channel, requiredPath: apiPath)
        let site = try validatedURL(values["WEBSITE_BASE_URL"]!, key: "WEBSITE_BASE_URL", channel: channel, requiredPath: nil)
        return ClientEnvironment(channel: channel, apiBase: api, websiteBase: site)
    }

    private static func isSafe(_ c: Character) -> Bool {
        guard c.isASCII, let scalar = c.unicodeScalars.first, scalar.value > 0x20, scalar.value < 0x7f else { return false }
        return !"\"'$`\\;|&<>(){}*!".contains(c)
    }

    private static func validatedURL(_ value: String, key: String, channel: BuildChannel, requiredPath: String?) throws -> URL {
        guard let components = URLComponents(string: value), let scheme = components.scheme?.lowercased(),
              let host = components.host, !host.isEmpty,
              components.user == nil, components.password == nil,
              components.query == nil, components.fragment == nil else { throw ClientEnvironmentError.invalidURL(key) }
        let loopback = ["localhost", "127.0.0.1", "::1"].contains(host.lowercased())
        switch scheme {
        case "https": break
        case "http" where channel == .development && loopback: break
        default: throw ClientEnvironmentError.invalidURL(key)
        }
        if let requiredPath {
            guard components.path == requiredPath else { throw ClientEnvironmentError.invalidURL(key) }
        } else {
            guard components.path.isEmpty || components.path == "/" else { throw ClientEnvironmentError.invalidURL(key) }
        }
        guard let url = components.url else { throw ClientEnvironmentError.invalidURL(key) }
        return url
    }
}

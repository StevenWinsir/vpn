import Foundation

/// The tunnel the Go Core asks the packet tunnel provider to establish
/// (shared/contracts/tun-spec-ios-v1.json). It is derived from the same Mihomo template the
/// stack runs, so address, prefix, MTU and resolver cannot drift. It never carries node or
/// account data. An iOS tunnel must always claim IPv6: without NEIPv6Settings IPv6 leaves
/// through the physical interface, so a spec without it is refused.
public struct TunnelSpec: Equatable, Sendable {
    public let address4: String
    public let prefix4: Int
    public let dns: String
    public let mtu: Int
    public let address6: String
    public let prefix6: Int

    static let fields: Set<String> = ["address4", "prefix4", "dns", "mtu", "ipv6", "address6", "prefix6"]

    public enum Failure: Error, Equatable {
        case malformed
        case unsafe(String)
    }

    /// Strict decoding: unknown fields, trailing data, hostnames and out-of-range values fail.
    public static func parse(_ json: String) throws -> TunnelSpec {
        let data = Data(json.utf8)
        guard data.count <= 4096,
              let object = try? JSONSerialization.jsonObject(with: data, options: []) as? [String: Any],
              Set(object.keys).isSubset(of: fields),
              let address4 = object["address4"] as? String, let prefix4 = integer(object["prefix4"]),
              let dns = object["dns"] as? String, let mtu = integer(object["mtu"]),
              let ipv6 = object["ipv6"] as? Bool
        else { throw Failure.malformed }
        guard ipv6, let address6 = object["address6"] as? String, let prefix6 = integer(object["prefix6"]) else {
            throw Failure.unsafe("an iOS tunnel must claim IPv6")
        }
        guard let v4 = IPv4(address4), (8...30).contains(prefix4), let resolver = IPv4(dns),
              resolver == v4.next, (1280...9000).contains(mtu),
              let v6 = IPv6(address6), (64...126).contains(prefix6), !v6.isIPv4Mapped
        else { throw Failure.unsafe("address, prefix, resolver or MTU out of range") }
        return TunnelSpec(address4: address4, prefix4: prefix4, dns: dns, mtu: mtu, address6: address6, prefix6: prefix6)
    }

    /// The NEPacketTunnelNetworkSettings values, without NetworkExtension so they are tested on
    /// any host (TunnelSettings.swift in the extension applies them one to one).
    public var plan: NetworkSettingsPlan {
        NetworkSettingsPlan(
            tunnelRemoteAddress: NetworkSettingsPlan.placeholderRemote,
            ipv4Addresses: [address4], ipv4SubnetMasks: [IPv4.mask(prefix4)], ipv4IncludedRoutes: ["0.0.0.0/0"],
            ipv6Addresses: [address6], ipv6PrefixLengths: [prefix6], ipv6IncludedRoutes: ["::/0"],
            dnsServers: [dns], dnsMatchDomains: [""], mtu: mtu, includeAllNetworks: false)
    }

    /// JSON numbers arrive as NSNumber; a Bool is an NSNumber too and must not pass as one.
    private static func integer(_ value: Any?) -> Int? {
        guard let number = value as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID() else { return nil }
        let int = number.intValue
        return NSNumber(value: int) == number ? int : nil
    }
}

/// What the provider passes to setTunnelNetworkSettings.
public struct NetworkSettingsPlan: Equatable, Sendable {
    /// Shown in Settings > VPN: a fixed placeholder, never a node address.
    public static let placeholderRemote = "127.0.0.1"

    public let tunnelRemoteAddress: String
    public let ipv4Addresses: [String]
    public let ipv4SubnetMasks: [String]
    public let ipv4IncludedRoutes: [String]
    public let ipv6Addresses: [String]
    public let ipv6PrefixLengths: [Int]
    public let ipv6IncludedRoutes: [String]
    public let dnsServers: [String]
    /// [""] makes the tunnel resolver answer every domain (system DNS goes into the tunnel).
    public let dnsMatchDomains: [String]
    public let mtu: Int
    /// Off (handoff_app_ios.md 8.2): the extension's own traffic and local services stay outside.
    public let includeAllNetworks: Bool
}

struct IPv4: Equatable {
    let value: UInt32

    init?(_ text: String) {
        let parts = text.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 4 else { return nil }
        var value: UInt32 = 0
        for part in parts {
            guard (1...3).contains(part.count), part.allSatisfy(\.isASCII), let octet = UInt8(part),
                  part == "0" || !part.hasPrefix("0") else { return nil }
            value = value << 8 | UInt32(octet)
        }
        self.value = value
    }

    private init(value: UInt32) { self.value = value }

    var next: IPv4 { IPv4(value: value &+ 1) }

    static func mask(_ prefix: Int) -> String {
        let bits: UInt32 = prefix == 0 ? 0 : ~UInt32(0) << UInt32(32 - prefix)
        return [24, 16, 8, 0].map { String((bits >> UInt32($0)) & 0xff) }.joined(separator: ".")
    }
}

struct IPv6 {
    let bytes: [UInt8]

    init?(_ text: String) {
        var storage = in6_addr()
        guard text.contains(":"), inet_pton(AF_INET6, text, &storage) == 1 else { return nil }
        bytes = withUnsafeBytes(of: &storage) { Array($0) }
    }

    var isIPv4Mapped: Bool { bytes[0..<10].allSatisfy { $0 == 0 } && bytes[10] == 0xff && bytes[11] == 0xff }
}

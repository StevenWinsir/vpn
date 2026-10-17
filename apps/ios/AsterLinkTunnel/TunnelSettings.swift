import AsterLinkKit
import NetworkExtension

/// Applies a NetworkSettingsPlan (derived from the Core's TunnelSpec, tun-spec-ios-v1) one to
/// one. The mapping itself is tested in AsterLinkKit; this file only constructs the
/// NetworkExtension objects.
enum TunnelSettings {
    static func make(_ plan: NetworkSettingsPlan) -> NEPacketTunnelNetworkSettings {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: plan.tunnelRemoteAddress)
        let ipv4 = NEIPv4Settings(addresses: plan.ipv4Addresses, subnetMasks: plan.ipv4SubnetMasks)
        ipv4.includedRoutes = plan.ipv4IncludedRoutes.map(route4)
        settings.ipv4Settings = ipv4
        let ipv6 = NEIPv6Settings(addresses: plan.ipv6Addresses, networkPrefixLengths: plan.ipv6PrefixLengths.map { NSNumber(value: $0) })
        ipv6.includedRoutes = plan.ipv6IncludedRoutes.map(route6)
        settings.ipv6Settings = ipv6
        let dns = NEDNSSettings(servers: plan.dnsServers)
        dns.matchDomains = plan.dnsMatchDomains
        settings.dnsSettings = dns
        settings.mtu = NSNumber(value: plan.mtu)
        return settings
    }

    private static func route4(_ cidr: String) -> NEIPv4Route {
        cidr == "0.0.0.0/0" ? .default() : NEIPv4Route(destinationAddress: String(cidr.split(separator: "/")[0]), subnetMask: "255.255.255.255")
    }

    private static func route6(_ cidr: String) -> NEIPv6Route {
        cidr == "::/0" ? .default() : NEIPv6Route(destinationAddress: String(cidr.split(separator: "/")[0]), networkPrefixLength: 128)
    }
}

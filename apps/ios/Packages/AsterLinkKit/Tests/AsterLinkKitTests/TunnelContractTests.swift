import Foundation
import Network
import Testing
@testable import AsterLinkKit

/// Round 2 contracts read in place: tun-spec-ios-v1.json (what the provider applies) and
/// handoff-v1.json (the app <-> extension messages). Go is tested against the same files.
struct TunnelContractTests {
    static func fixture(_ name: String) throws -> [String: Any] {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let data = try Data(contentsOf: root.appendingPathComponent("shared/contracts/\(name)"))
        return try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    static func specs(_ contract: [String: Any], _ key: String) -> [String] {
        (contract[key] as? [[String: Any]] ?? []).compactMap { $0["spec"] as? String }
    }

    @Test func tunSpecAcceptsRejectsAndMapsExactly() throws {
        let contract = try Self.fixture("tun-spec-ios-v1.json")
        let accept = Self.specs(contract, "accept") + Self.specs(contract, "produced")
        let reject = Self.specs(contract, "reject")
        #expect(accept.count >= 5 && reject.count >= 10)
        for spec in accept {
            #expect(throws: Never.self, "\(spec)") { _ = try TunnelSpec.parse(spec) }
        }
        for spec in reject {
            #expect(throws: TunnelSpec.Failure.self, "\(spec)") { _ = try TunnelSpec.parse(spec) }
        }

        let expected = try #require(contract["network_settings"] as? [String: Any])
        let plan = try TunnelSpec.parse(try #require(expected["spec"] as? String)).plan
        #expect(plan.tunnelRemoteAddress == expected["tunnel_remote_address"] as? String)
        #expect(plan.ipv4Addresses == expected["ipv4_addresses"] as? [String])
        #expect(plan.ipv4SubnetMasks == expected["ipv4_subnet_masks"] as? [String])
        #expect(plan.ipv4IncludedRoutes == expected["ipv4_included_routes"] as? [String])
        #expect(plan.ipv6Addresses == expected["ipv6_addresses"] as? [String])
        #expect(plan.ipv6PrefixLengths == expected["ipv6_prefix_lengths"] as? [Int])
        #expect(plan.ipv6IncludedRoutes == expected["ipv6_included_routes"] as? [String])
        #expect(plan.dnsServers == expected["dns_servers"] as? [String])
        #expect(plan.dnsMatchDomains == expected["dns_match_domains"] as? [String])
        #expect(plan.mtu == expected["mtu"] as? Int)
        #expect(plan.includeAllNetworks == expected["include_all_networks"] as? Bool)
    }

    @Test func subnetMasks() {
        #expect(IPv4.mask(30) == "255.255.255.252")
        #expect(IPv4.mask(24) == "255.255.255.0")
        #expect(IPv4.mask(8) == "255.0.0.0")
        #expect(IPv4("198.18.0.1")?.next == IPv4("198.18.0.2"))
        #expect(IPv4("01.2.3.4") == nil && IPv4("1.2.3") == nil && IPv4("256.1.1.1") == nil)
    }

    @Test func providerMessagesRoundTripAndRefuseEverythingElse() throws {
        let contract = try Self.fixture("handoff-v1.json")
        let messages = try #require(contract["provider_messages"] as? [String: Any])
        let requests = try #require(messages["requests"] as? [[String: String]])
        #expect(requests.count == 5)
        var decoded: [ProviderMessage] = []
        for request in requests {
            let message = try ProviderMessage.decode(Data(try #require(request["message"]).utf8))
            decoded.append(message)
            // What the app encodes, the extension decodes to the same message.
            #expect(try ProviderMessage.decode(message.encoded()) == message)
        }
        #expect(decoded[1] == .release)
        #expect(decoded[2] == .foreground(visible: true) && decoded[3] == .foreground(visible: false))
        #expect(decoded[4] == .routeProbe(ok: true))
        guard case .ipc(let request) = decoded[0] else { Issue.record("ipc"); return }
        let body = try #require(try JSONSerialization.jsonObject(with: Data(request.utf8)) as? [String: Any])
        #expect(body["command"] as? String == "read_snapshot" && body["request_id"] as? String == "r1")

        for bad in try #require(messages["reject"] as? [[String: String]]) {
            #expect(throws: ProviderMessage.Failure.self, "\(bad["name"] ?? "")") {
                _ = try ProviderMessage.decode(Data(bad["message"]!.utf8))
            }
        }
        #expect(throws: ProviderMessage.Failure.self) {
            _ = try ProviderMessage.decode(Data(repeating: 0x20, count: ProviderMessage.maxSize + 1))
        }
    }

    @Test func providerRepliesRoundTrip() throws {
        let contract = try Self.fixture("handoff-v1.json")
        let messages = try #require(contract["provider_messages"] as? [String: Any])
        let replies = try #require(messages["replies"] as? [[String: String]])
        let decoded = try replies.map { try ProviderReply.decode(Data(try #require($0["reply"]).utf8)) }
        #expect(decoded == [.released(ticket: "<opaque ticket document>"), .error(code: "handoff_unsettled"), .ack, .invalidRequest])
        for reply in decoded {
            #expect(try ProviderReply.decode(reply.encoded()) == reply)
        }
        #expect(throws: ProviderMessage.Failure.self) { _ = try ProviderReply.decode(Data(#"{"kind":"released"}"#.utf8)) }
        #expect(contract["start_options_key"] as? String == TunnelStartOptions.handoffKey)
    }

    @Test func utunSelection() throws {
        let utun: UInt32 = 7
        let only = [UtunDescriptor.Candidate(fd: 9, controlID: utun, interfaceName: "utun4"),
                    UtunDescriptor.Candidate(fd: 3, controlID: 2, interfaceName: nil)]
        #expect(try UtunDescriptor.select(only, utunControlID: utun) == 9)
        #expect(throws: UtunDescriptor.Failure.notFound) { _ = try UtunDescriptor.select([], utunControlID: utun) }
        #expect(throws: UtunDescriptor.Failure.notFound) {
            _ = try UtunDescriptor.select([.init(fd: 4, controlID: 3, interfaceName: "utun1"), .init(fd: 5, controlID: utun, interfaceName: "ipsec0")],
                                          utunControlID: utun)
        }
        #expect(throws: UtunDescriptor.Failure.ambiguous([9, 12])) {
            _ = try UtunDescriptor.select(only + [.init(fd: 12, controlID: utun, interfaceName: "utun5")], utunControlID: utun)
        }
        // The kernel ABI: struct ctl_info is 100 bytes, struct sockaddr_ctl 32.
        #expect(MemoryLayout<UtunDescriptor.CtlInfo>.size == 100)
        #expect(MemoryLayout<UtunDescriptor.SockaddrCtl>.size == 32)
    }

    @Test func hostNetworkReportMatchesTheCoreFormat() throws {
        #expect(HostNetworkReport.unavailable.json() == #"{"available":false}"#)
        let report = HostNetworkReport(available: true, interface: "en0", index: 4, addresses: ["192.0.2.7", "2001:db8::7"],
                                       dns: ["192.0.2.1"], metered: true)
        let object = try #require(try JSONSerialization.jsonObject(with: Data(report.json().utf8)) as? [String: Any])
        // Exactly the fields network.HostSnapshot decodes (unknown fields are refused there).
        #expect(Set(object.keys) == ["available", "interface", "index", "addresses", "dns", "metered"])
        #expect(object["index"] as? Int == 4 && object["metered"] as? Bool == true)
        #expect(HostNetworkReport.isTunnelName("utun3") && !HostNetworkReport.isTunnelName("en0") && !HostNetworkReport.isTunnelName("pdp_ip0"))
        // The resolver list of this host is readable and numeric.
        for server in HostNetworkReport.systemResolvers() {
            #expect(server.contains(".") || server.contains(":"), "\(server)")
        }
        #expect(!HostNetworkReport.addresses(of: "lo0").isEmpty)
    }

    @Test func memoryBudgetThresholds() {
        var budget = MemoryBudget()
        #expect(budget.observe(20 << 20) == .none)
        #expect(budget.observe(40 << 20) == .release)
        #expect(budget.observe(45 << 20) == .pressure)
        #expect(budget.observe(10 << 20) == .none)
        #expect(budget.peak == 45 << 20)
        #expect((MemoryBudget.footprint() ?? 0) > 0)
        #expect(MemoryBudget.goHeapLimit < Int64(MemoryBudget.softLimit))
    }

    @Test func routeProbeNeedsTheControlledAnswer() {
        #expect(RouteProbe.accepts(status: 204, bodyLength: 0))
        #expect(!RouteProbe.accepts(status: 200, bodyLength: 0))
        #expect(!RouteProbe.accepts(status: 204, bodyLength: 12))
        #expect(!RouteProbe.accepts(status: 302, bodyLength: 0))
    }
}

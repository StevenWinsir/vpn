import Foundation

/// The text behind "复制诊断信息" (handoff_app_ios.md 10.3). It is built only from facts that are
/// already on screen or are build identity: no token, password, node address, port, YAML or
/// credential can reach it, because none of them exist in the types it reads.
public enum Diagnostics {
    public static func report(appVersion: String, channel: BuildChannel, state: SessionState, now: Date = Date()) -> String {
        let snapshot = state.snapshot
        var lines = [
            "AsterLink 诊断信息",
            "时间: \(Formatting.dateTime(now))",
            "应用版本: \(appVersion)（\(channel.rawValue)）",
        ]
        if let identity = state.identity {
            lines.append("Core: \(identity.coreVersion)  协议: \(identity.protocolVersion)")
            lines.append("Mihomo: \(String(identity.mihomoRevision.prefix(12)))")
        }
        lines.append("状态: \(snapshot.state.isEmpty ? "-" : snapshot.state)  隧道: \(snapshot.tunnelHealth.isEmpty ? "-" : snapshot.tunnelHealth)  代次: \(snapshot.generation)")
        lines.append("线路数: \(snapshot.nodes.count)  已选线路: \(snapshot.selectedNodeID.isEmpty ? "无" : "是")  配置就绪: \(snapshot.configReady ? "是" : "否")")
        if let entitlement = snapshot.entitlement {
            lines.append("套餐: \(entitlement.planName.isEmpty ? "无" : entitlement.planName)  可用: \(entitlement.accountAuthorized ? "是" : "否")  原因: \(entitlement.reason.isEmpty ? "-" : entitlement.reason)")
        }
        if let error = snapshot.lastError, !error.code.isEmpty { lines.append("最近错误: \(error.code)") }
        if let gate = snapshot.gate { lines.append("闸门: \(gate.reason)  等待网络: \(gate.waitingNetwork ? "是" : "否")") }
        return lines.joined(separator: "\n")
    }
}

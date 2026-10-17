#if DEBUG
import AsterLinkKit
import NetworkExtension
import SwiftUI

/// Round-2 debug controls (handoff_app_ios.md round 2: "App 侧最小连接控制（调试界面即可）"): sign in
/// with the app's Core, pick a line, install the VPN configuration, hand the session to the
/// extension, read its snapshot, take the session back, and run Verify's second stage. Debug
/// builds only; the round-3 interface replaces it. The password lives in this view's state
/// only while typed; nothing secret is logged or shown.
struct TunnelDebugView: View {
    @State private var email = ""
    @State private var password = ""
    @State private var lines: [String] = []
    @State private var head: SnapshotHead?
    @State private var busy = false
    @State private var tunnel = TunnelController()

    var body: some View {
        Form {
            Section("账号（App 内核）") {
                TextField("邮箱", text: $email).textContentType(.username).textInputAutocapitalization(.never)
                    .accessibilityIdentifier("debug-email")
                SecureField("密码", text: $password).textContentType(.password).accessibilityIdentifier("debug-password")
                Button("登录") { run { try await login() } }.accessibilityIdentifier("debug-login")
                Button("选择第一条线路") { run { try await selectFirst() } }
            }
            Section("系统 VPN（扩展）") {
                Button("安装 VPN 配置") { run { try await tunnel.install(); note("VPN 配置已安装") } }
                Button("连接（交出会话）") { run { try await tunnel.connect(); note("已交给扩展，状态 \(tunnel.status.rawValue)") } }
                Button("读取扩展快照") { run { try await readExtension() } }
                Button("App 路由探测（Verify 第二段）") { run { try await probe() } }
                Button("断开并取回会话") { run { head = try await tunnel.disconnect(); note("已取回：\(head?.state ?? "?")") } }
                Button("取回交出的票据") { run { await takeBack() } }
                Button("上次断开原因") { run { note("原因：\(await tunnel.lastDisconnectReason() ?? "无")") } }
            }
            Section("状态") {
                Text("App 内核：\(head?.state ?? "未启动") · 线路 \(head?.selectedNodeID.prefix(8) ?? "-")")
                    .font(.footnote.monospaced()).accessibilityIdentifier("debug-state")
                ForEach(Array(lines.suffix(12).enumerated()), id: \.offset) { _, line in
                    Text(line).font(.caption.monospaced())
                }
            }
        }
        .disabled(busy)
        .task {
            if (try? CoreLifecycle.start()) != nil { note("App 内核已启动") }
            try? await tunnel.load()
            note(tunnel.manager == nil ? "尚未安装 VPN 配置" : "已找到 VPN 配置")
        }
    }

    private func run(_ action: @escaping @MainActor () async throws -> Void) {
        busy = true
        Task { @MainActor in
            do { try await action() } catch { note("失败：\(Self.describe(error))") }
            busy = false
        }
    }

    private func note(_ line: String) { lines.append(line) }

    private static func describe(_ error: Error) -> String {
        switch error {
        case TunnelController.Failure.core(let failure): return "\(failure.code) \(failure.message)"
        case TunnelController.Failure.extensionRefused(let code): return "扩展拒绝 \(code)"
        case TunnelController.Failure.system(let code): return "系统错误 \(code)"
        case TunnelController.Failure.noConfiguration: return "未安装 VPN 配置"
        default: return String(describing: type(of: error))
        }
    }

    private func login() async throws {
        let info = Bundle.main.infoDictionary ?? [:]
        let apiBase = info["AsterAPIBaseURL"] as? String ?? ""
        let channel: BuildChannel = (info["AsterAppEnv"] as? String) == "release" ? .release : .development
        let deviceID = try DeviceIdentity.load(service: (Bundle.main.bundleIdentifier ?? "asterlink") + ".device")
        let request = CoreRequest.login(requestID: "debug-login", email: email, password: password, apiBase: apiBase,
                                        channel: channel, deviceID: deviceID, appVersion: info["CFBundleShortVersionString"] as? String ?? "0")
        password = ""
        try await apply(Task.detached { CoreBridge.call(request) }.value)
    }

    private func selectFirst() async throws {
        guard let head, let node = head.nodes.first else { note("没有可选线路"); return }
        let request = CoreRequest.selectNode(requestID: "debug-select", generation: head.generation, nodeID: node.id, catalogRevision: head.catalogRevision)
        try await apply(Task.detached { CoreBridge.call(request) }.value)
    }

    private func readExtension() async throws {
        let raw = try await tunnel.call(CoreRequest.command("read_snapshot", requestID: "debug-read"))
        let decoded = CoreBridge.decode(raw, as: SnapshotHead.self)
        switch decoded {
        case .success(let snapshot): note("扩展：\(snapshot.state) · \(snapshot.tunnelHealth)")
        case .failure(let failure): note("扩展：\(failure.code)")
        }
    }

    private func probe() async throws {
        let ok = await RouteProbe.run()
        _ = try await tunnel.send(.routeProbe(ok: ok))
        note(ok ? "路由探测通过（204）" : "路由探测未通过")
    }

    private func takeBack() async {
        switch await tunnel.takeBack() {
        case .success(let snapshot)?: head = snapshot; note("已取回：\(snapshot.state)")
        case .failure(let failure)?: note("取回失败：\(failure.code)")
        case nil: note("没有交出的票据")
        }
    }

    private func apply(_ raw: String) async throws {
        switch CoreBridge.decode(raw, as: SnapshotHead.self) {
        case .success(let snapshot):
            head = snapshot
            note("\(snapshot.state)\(snapshot.actionError.map { " · \($0.code)" } ?? "")")
        case .failure(let failure):
            throw TunnelController.Failure.core(failure)
        }
    }
}
#endif

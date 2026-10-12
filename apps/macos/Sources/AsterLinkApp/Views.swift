import AsterLinkShared
import Charts
import SwiftUI

struct RootView: View {
    @Bindable var session: SessionController
    let helper: HelperClient
    let diagnostics: AppDiagnostics
    @State private var showingSettings = false

    var body: some View {
        Group {
            switch session.phase {
            case .starting:
                ProgressView("正在启动…").frame(maxWidth: .infinity, maxHeight: .infinity)
            case .signedOut, .signingIn:
                LoginView(session: session, helper: helper, showSettings: { showingSettings = true })
            case .signedIn:
                MainView(session: session, showSettings: { showingSettings = true })
            }
        }
        .frame(minWidth: 460, idealWidth: 480, minHeight: 520)
        .sheet(isPresented: $showingSettings) {
            SettingsView(session: session, helper: helper, diagnostics: diagnostics)
        }
        .task { await session.launch() }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await session.refresh() }
        }
        .onReceive(NSWorkspace.shared.notificationCenter.publisher(for: NSWorkspace.didWakeNotification)) { _ in
            // After sleep nothing cached is trusted: re-sync catalog and authorization at once.
            Task { await session.refresh() }
        }
    }
}

// MARK: - Login

struct LoginView: View {
    @Bindable var session: SessionController
    let helper: HelperClient
    let showSettings: () -> Void
    @State private var password = ""

    var body: some View {
        VStack(spacing: 18) {
            Spacer(minLength: 8)
            Image(systemName: "bolt.shield.fill").font(.system(size: 44)).foregroundStyle(.tint)
            Text("AsterLink").font(.largeTitle.bold())

            VStack(spacing: 10) {
                TextField("邮箱", text: $session.email)
                    .textFieldStyle(.roundedBorder).textContentType(.username).autocorrectionDisabled()
                SecureField("密码", text: $password)
                    .textFieldStyle(.roundedBorder).textContentType(.password)
                    .onSubmit(submit)
                Toggle("记住密码", isOn: $session.rememberPassword).frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxWidth: 320)

            Button(action: submit) {
                if session.phase == .signingIn { ProgressView().controlSize(.small) } else { Text("登录").frame(maxWidth: .infinity) }
            }
            .buttonStyle(.borderedProminent).controlSize(.large).frame(maxWidth: 320)
            .keyboardShortcut(.defaultAction)
            .disabled(session.phase == .signingIn || session.email.isEmpty || password.isEmpty)

            if helper.registration != .enabled {
                VStack(spacing: 6) {
                    Label("需要先启用特权服务", systemImage: "lock.shield").foregroundStyle(.orange)
                    Text(helper.registration.summary).font(.caption).foregroundStyle(.secondary)
                    HStack {
                        Button("启用") { helper.register() }
                        Button("打开系统设置") { helper.openSystemSettings() }
                        Button("刷新") { helper.refreshRegistration() }
                    }
                }
            }
            if let problem = session.message ?? session.serviceProblem {
                Text(problem).foregroundStyle(.red).multilineTextAlignment(.center).textSelection(.enabled)
            }
            if let warning = session.warning { Text(warning).foregroundStyle(.orange).font(.callout) }

            HStack(spacing: 18) {
                Link("注册账号", destination: session.registerURL)
                Link("购买套餐", destination: session.plansURL)
                Button("设置", action: showSettings).buttonStyle(.link)
            }.font(.callout)
            Spacer()
        }
        .padding(24)
    }

    private func submit() {
        let secret = password
        Task {
            await session.signIn(email: session.email, password: secret, remember: session.rememberPassword)
            if session.phase == .signedIn { password = "" }
        }
    }
}

// MARK: - Main

struct MainView: View {
    @Bindable var session: SessionController
    let showSettings: () -> Void

    private var snap: CoreSnapshot { session.snapshot }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            header
            planCard
            nodeList
            connectBar
            if let message = session.message {
                Text(message).font(.callout).foregroundStyle(.orange).textSelection(.enabled)
            }
        }
        .padding(18)
    }

    private var header: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(snap.user?.email ?? "").font(.headline)
                if snap.user?.role == "admin" { Text("管理员账号").font(.caption).foregroundStyle(.secondary) }
            }
            Spacer()
            if session.busy { ProgressView().controlSize(.small) }
            Button { showSettings() } label: { Image(systemName: "gearshape") }.buttonStyle(.borderless)
        }
    }

    private var planCard: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 6) {
                if let entitlement = snap.entitlement, entitlement.accountAuthorized {
                    HStack {
                        Text(entitlement.planName.isEmpty ? "套餐" : entitlement.planName).font(.headline)
                        Spacer()
                        Text("到期：\(Formatting.date(entitlement.subscriptionExpiresAt))").font(.callout).foregroundStyle(.secondary)
                    }
                    ProgressView(value: usedFraction(entitlement))
                    Text("剩余 \(Formatting.bytes(entitlement.remainingBytes)) / 共 \(Formatting.bytes(entitlement.totalBytes))")
                        .font(.callout).foregroundStyle(.secondary)
                } else {
                    Label(snap.entitlement?.reasonMessage.isEmpty == false ? snap.entitlement!.reasonMessage : "暂无可用套餐",
                          systemImage: "exclamationmark.triangle").foregroundStyle(.orange)
                    HStack {
                        Link("购买 / 续费", destination: session.plansURL)
                        Button("刷新") { Task { await session.refresh() } }.buttonStyle(.link)
                    }.font(.callout)
                }
            }.frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private func usedFraction(_ e: CoreEntitlement) -> Double {
        guard e.totalBytes > 0 else { return 0 }
        return min(1, max(0, 1 - Double(e.remainingBytes) / Double(e.totalBytes)))
    }

    private var nodeList: some View {
        GroupBox("线路") {
            if snap.nodes.isEmpty {
                Text(snap.entitlement?.accountAuthorized == true ? "管理员尚未配置可用线路" : "购买套餐后可选择线路")
                    .foregroundStyle(.secondary).frame(maxWidth: .infinity, minHeight: 60)
            } else {
                ScrollView {
                    VStack(spacing: 6) {
                        ForEach(snap.nodes) { node in
                            NodeRow(node: node, selected: node.id == snap.selectedNodeID, ready: node.id == snap.selectedNodeID && snap.configReady) {
                                Task { await session.select(node.id) }
                            }
                        }
                    }.padding(.vertical, 2)
                }.frame(minHeight: 120, maxHeight: 260)
            }
        }
    }

    private var connectBar: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Circle().fill(stateColor).frame(width: 10, height: 10)
                Text(stateText).font(.headline)
                Spacer()
                switch snap.state {
                case .connected:
                    Button("断开") { Task { await session.disconnect() } }.disabled(session.busy)
                case .connecting, .preparing, .stopping:
                    ProgressView().controlSize(.small)
                default:
                    Button("连接") { Task { await session.connect() } }
                        .buttonStyle(.borderedProminent)
                        .disabled(snap.selectedNodeID.isEmpty || snap.entitlement?.accountAuthorized != true || session.busy)
                }
            }
            if snap.state != .connected, let failure = snap.lastError, session.message == nil {
                Label(failure.message, systemImage: "exclamationmark.triangle").font(.callout).foregroundStyle(.orange)
            }
            if let metering = snap.metering, snap.state == .connected || metering.uploadBytes + metering.downloadBytes > 0 {
                meteringCard(metering)
            }
        }
    }

    private func meteringCard(_ metering: CoreMetering) -> some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 16) {
                    Label("↑ \(Formatting.bytes(metering.uploadBytesPerSecond))/s", systemImage: "arrow.up")
                    Label("↓ \(Formatting.bytes(metering.downloadBytesPerSecond))/s", systemImage: "arrow.down")
                    Spacer()
                    Text("线路倍率 \(CoreNode.formatRate(metering.ratePermille))").foregroundStyle(.secondary)
                }
                if !metering.history.isEmpty {
                    Chart {
                        ForEach(Array(metering.history.enumerated()), id: \.offset) { index, sample in
                            LineMark(x: .value("秒", index), y: .value("下行", sample.download), series: .value("方向", "下行"))
                                .foregroundStyle(by: .value("方向", "下行"))
                            LineMark(x: .value("秒", index), y: .value("上行", sample.upload), series: .value("方向", "上行"))
                                .foregroundStyle(by: .value("方向", "上行"))
                        }
                    }
                    .chartXAxis(.hidden)
                    .chartYAxis(.hidden)
                    .chartLegend(.hidden)
                    .frame(height: 44)
                }
                Text("本次登录已经线路传输：上行 \(Formatting.bytes(metering.uploadBytes)) · 下行 \(Formatting.bytes(metering.downloadBytes))")
                    .font(.callout).foregroundStyle(.secondary)
                Text("预估剩余套餐流量 \(Formatting.bytes(metering.estimatedRemainingBytes))（服务器已确认 \(Formatting.bytes(metering.confirmedRemainingBytes))\(metering.pending ? "，有批次待确认" : "")）")
                    .font(.callout).foregroundStyle(.secondary)
                if metering.source == "node_reported" {
                    // The node bills from its own counters; the figures above are only what this Mac measured.
                    Text("本线路由节点侧独立计量：套餐流量按节点实测的收发字节扣除，本机数值仅供参考。")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }.frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var stateText: String {
        switch snap.state {
        case .connected: "已连接"
        case .connecting: "正在连接…"
        case .preparing: "正在准备…"
        case .stopping: "正在断开…"
        case .blocked: "不可用"
        case .error: "出错"
        case .ready: snap.configReady ? "已就绪，未连接" : "未连接"
        default: "未连接"
        }
    }

    private var stateColor: Color {
        switch snap.state {
        case .connected: .green
        case .connecting, .preparing, .stopping: .yellow
        case .blocked, .error: .red
        default: .gray
        }
    }
}

struct NodeRow: View {
    let node: CoreNode
    let selected: Bool
    let ready: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Image(systemName: selected ? "largecircle.fill.circle" : "circle").foregroundStyle(selected ? Color.accentColor : .secondary)
                VStack(alignment: .leading, spacing: 2) {
                    Text(node.name).font(.body)
                    Text(node.region).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Text(node.lineLabel).font(.caption.bold()).padding(.horizontal, 7).padding(.vertical, 2)
                    .background(node.isDedicated ? Color.orange.opacity(0.2) : Color.blue.opacity(0.15), in: Capsule())
                Text("流量 \(node.rateLabel)").font(.caption).foregroundStyle(.secondary)
                if node.isNodeMetered { Image(systemName: "checkmark.shield").foregroundStyle(.secondary).help("节点侧独立计量") }
                if ready { Image(systemName: "checkmark.seal.fill").foregroundStyle(.green).help("线路配置已校验") }
            }
            .contentShape(Rectangle())
            .padding(.horizontal, 8).padding(.vertical, 6)
            .background(selected ? Color.accentColor.opacity(0.1) : .clear, in: RoundedRectangle(cornerRadius: 8))
        }.buttonStyle(.plain)
    }
}

// MARK: - Settings

struct SettingsView: View {
    @Bindable var session: SessionController
    let helper: HelperClient
    let diagnostics: AppDiagnostics
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("设置与关于").font(.title2.bold())
            GroupBox("特权服务（Helper）") {
                VStack(alignment: .leading, spacing: 8) {
                    Text(helper.registration.summary)
                    HStack {
                        Button("启用") { helper.register() }.disabled(helper.registration == .enabled)
                        Button("打开系统设置") { helper.openSystemSettings() }
                        Button("刷新") { helper.refreshRegistration() }
                        Button("握手") { helper.hello() }.disabled(helper.registration != .enabled || helper.busy)
                    }
                    if let reply = helper.lastReply {
                        Text("Helper \(reply.helperVersion) · 协议 v\(reply.protocolVersion) · 内核运行：\(reply.workerRunning ? "是" : "否")").font(.caption)
                    }
                    if let error = helper.lastError { Text(error).font(.caption).foregroundStyle(.red) }
                }.frame(maxWidth: .infinity, alignment: .leading)
            }
            GroupBox("版本") {
                VStack(alignment: .leading, spacing: 4) {
                    Text("应用 \(diagnostics.appVersion) · 通道 \(diagnostics.channel)")
                    Text("内核 \(session.snapshot.coreVersion.isEmpty ? "—" : session.snapshot.coreVersion) · Mihomo \(diagnostics.mihomoRevision)")
                    Text("服务器 \(diagnostics.apiHost)")
                }.font(.callout).frame(maxWidth: .infinity, alignment: .leading)
            }
            GroupBox("网站") {
                HStack {
                    Link("控制台", destination: session.websiteBase.appendingPathComponent("dashboard"))
                    Link("套餐", destination: session.plansURL)
                    Link("订单", destination: session.websiteBase.appendingPathComponent("orders"))
                }.frame(maxWidth: .infinity, alignment: .leading)
            }
            HStack {
                Button("复制脱敏诊断") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(diagnostics.report(for: session.snapshot), forType: .string)
                }
                Spacer()
                if session.phase == .signedIn {
                    Button("退出登录", role: .destructive) {
                        Task { await session.signOut(); dismiss() }
                    }
                }
                Button("关闭") { dismiss() }.keyboardShortcut(.cancelAction)
            }
        }
        .padding(20).frame(width: 460)
    }
}

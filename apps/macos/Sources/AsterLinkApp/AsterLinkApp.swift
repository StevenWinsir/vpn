import AsterLinkShared
import SwiftUI

/// Stage 1 shell: proves the App -> Helper (XPC) -> Core (pipe) chain and the Helper
/// authorization states. Login, nodes and connect arrive in later stages.
@main
struct AsterLinkApp: App {
    @StateObject private var helper: HelperClient
    private let environment: ClientEnvironment?

    init() {
        let info = Bundle.main.infoDictionary ?? [:]
        let channel = BuildChannel(rawValue: (info["AsterLinkChannel"] as? String) ?? "development") ?? .development
        let team = (info["AsterLinkTeamID"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        let policy = (try? SigningPolicy(channel: channel, teamID: team)) ?? (try! SigningPolicy(channel: .development, teamID: nil))
        _helper = StateObject(wrappedValue: HelperClient(policy: policy))
        if let api = info["AsterLinkAPIBase"] as? String, let site = info["AsterLinkWebsiteBase"] as? String {
            // Re-validate with the same rules as the build script; never trust the plist blindly.
            environment = try? ClientEnvironment.parse("APP_ENV=\(channel.rawValue)\nAPI_BASE_URL=\(api)\nWEBSITE_BASE_URL=\(site)\n")
        } else {
            environment = nil
        }
    }

    var body: some Scene {
        WindowGroup("AsterLink") {
            StatusView(helper: helper, environment: environment)
                .frame(minWidth: 460, minHeight: 360)
        }
        .windowResizability(.contentSize)
    }
}

struct StatusView: View {
    @ObservedObject var helper: HelperClient
    let environment: ClientEnvironment?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("AsterLink").font(.title.bold())
            Text("阶段 1：特权服务与内核握手验证（尚未登录/连接）").foregroundStyle(.secondary)
            GroupBox("构建配置") {
                if let environment {
                    VStack(alignment: .leading) {
                        Text("通道：\(environment.channel.rawValue)")
                        Text("API：\(environment.apiBase.absoluteString)").textSelection(.enabled)
                    }.frame(maxWidth: .infinity, alignment: .leading)
                } else {
                    Text("配置缺失或无效，拒绝连接").foregroundStyle(.red)
                }
            }
            GroupBox("特权服务（Helper）") {
                VStack(alignment: .leading, spacing: 8) {
                    Text(label(helper.registration))
                    HStack {
                        Button("启用") { helper.register() }.disabled(helper.registration == .enabled)
                        Button("打开系统设置") { helper.openSystemSettings() }
                        Button("刷新") { helper.refreshRegistration() }
                    }
                    HStack {
                        Button("握手 Helper") { helper.hello() }
                        Button("启动并握手 Core") { helper.startWorker() }
                        Button("停止 Core") { helper.stopWorker() }
                    }.disabled(helper.registration != .enabled || helper.busy)
                }.frame(maxWidth: .infinity, alignment: .leading)
            }
            if let reply = helper.lastReply {
                GroupBox("最近一次回复") {
                    VStack(alignment: .leading) {
                        Text("Helper \(reply.helperVersion) · 协议 v\(reply.protocolVersion) · Core 运行：\(reply.workerRunning ? "是" : "否")")
                        if let core = reply.core {
                            Text("Core \(core.coreVersion) · Mihomo \(core.mihomoRevision.prefix(10))")
                        }
                    }.frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            if let error = helper.lastError {
                Text(error).foregroundStyle(.red).textSelection(.enabled)
            }
            Spacer()
        }
        .padding(20)
    }

    private func label(_ state: HelperRegistration) -> String {
        switch state {
        case .enabled: "已启用"
        case .requiresApproval: "需要在“系统设置 › 通用 › 登录项”中批准"
        case .notRegistered: "未启用"
        case .notFound: "未找到服务（请使用完整签名的 App 包运行）"
        case .unknown: "未知"
        }
    }
}

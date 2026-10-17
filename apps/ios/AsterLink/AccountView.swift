import AsterLinkKit
import NetworkExtension
import SwiftUI
import UserNotifications

/// The account tab: who is signed in, the plan, appearance, system permissions, about, sign out.
struct AccountView: View {
    @Environment(AppModel.self) private var model
    let session: SessionController

    @State private var confirmSignOut = false
    @State private var notifications = "—"
    @State private var vpn = TunnelController()
    @State private var vpnInstalled: Bool?
    @State private var copied = false
    @State private var showLicenses = false

    private var state: SessionState { session.state }
    private var snapshot: CoreSnapshot { state.snapshot }

    var body: some View {
        List {
            if let message = state.message {
                Banner(kind: .error, text: ErrorText.text(for: message), identifier: "message-banner") { session.dismissMessage() }
                    .listRowBackground(Color.clear).listRowSeparator(.hidden)
            }
            Section { accountCard }.listRowBackground(Color.clear).listRowInsets(.init())
            Section { planCard }.listRowBackground(Color.clear).listRowInsets(.init())
            Section {
                Picker("外观", selection: Bindable(model).appearance) {
                    ForEach(Appearance.allCases) { Text($0.title).tag($0) }
                }
                .accessibilityIdentifier("appearance-picker")
            }
            .listRowBackground(Theme.surface)
            Section {
                LabeledContent("通知", value: notifications).accessibilityIdentifier("notifications-status")
                LabeledContent("VPN 配置", value: vpnText).accessibilityIdentifier("vpn-status")
                if vpnInstalled == true {
                    Button("移除 VPN 配置", role: .destructive) { Task { try? await vpn.remove(); vpnInstalled = vpn.manager != nil } }
                        .accessibilityIdentifier("vpn-remove")
                }
            } header: { SectionTitle(text: "系统权限") } footer: {
                Text("首次连接时，系统会请求添加 VPN 配置；AsterLink 不会保存系统密码。").font(.footnote)
            }
            .listRowBackground(Theme.surface)
            about.listRowBackground(Theme.surface)
            Section {
                Button(role: .destructive) { confirmSignOut = true } label: { Text("退出登录").frame(maxWidth: .infinity, minHeight: 44) }
                    .accessibilityIdentifier("sign-out")
            }
            .listRowBackground(Theme.surface)
            #if DEBUG
            Section("调试") { NavigationLink("隧道调试（第 2 轮）") { TunnelDebugView() }.accessibilityIdentifier("open-tunnel-debug") }
                .listRowBackground(Theme.surface)
            #endif
        }
        .scrollContentBackground(.hidden)
        .background(Theme.background.ignoresSafeArea())
        .navigationTitle("我的")
        .task { await loadPermissions() }
        .confirmationDialog("退出登录？", isPresented: $confirmSignOut, titleVisibility: .visible) {
            Button("退出并忘记本机密码", role: .destructive) { Task { await session.signOut() } }
            Button("取消", role: .cancel) {}
        } message: {
            Text("退出后会断开连接并结算流量，已保存的密码也会从本机删除。")
        }
        .sheet(isPresented: $showLicenses) { LicensesView() }
    }

    private var accountCard: some View {
        let user = snapshot.user
        return Card {
            HStack(spacing: 14) {
                Text(Formatting.initial(name: user?.name ?? "", email: user?.email ?? ""))
                    .font(.title2.weight(.semibold)).foregroundStyle(.white)
                    .frame(width: 52, height: 52).background(Circle().fill(Theme.primary)).accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 4) {
                    Text(user?.email ?? "").font(.headline).foregroundStyle(Theme.textPrimary).accessibilityIdentifier("account-email")
                    if user?.role == "admin" {
                        Text("管理员账号").font(.footnote).foregroundStyle(Theme.textSecondary).accessibilityIdentifier("account-role")
                    }
                }
                Spacer(minLength: 0)
            }
        }
        .padding(.vertical, 4)
    }

    @ViewBuilder private var planCard: some View {
        let entitlement = snapshot.entitlement
        Card {
            VStack(alignment: .leading, spacing: 10) {
                Text("套餐").font(.footnote).foregroundStyle(Theme.textSecondary)
                if let entitlement, entitlement.accountAuthorized || !entitlement.planName.isEmpty {
                    Text(entitlement.planName.isEmpty ? "无有效套餐" : entitlement.planName).font(.headline).foregroundStyle(Theme.textPrimary)
                        .accessibilityIdentifier("account-plan")
                    if let expires = entitlement.subscriptionExpires {
                        let days = Formatting.daysLeft(expires, now: Date()) ?? 0
                        Text("\(Formatting.date(expires)) 到期" + (days > 0 ? " · 还剩 \(days) 天" : " · 已到期"))
                            .font(.subheadline).foregroundStyle(days > 0 ? Theme.textSecondary : Theme.error)
                            .accessibilityIdentifier("account-expiry")
                    }
                    if entitlement.totalBytes > 0 {
                        ProgressView(value: 1 - Formatting.usedFraction(totalBytes: entitlement.totalBytes, remainingBytes: entitlement.remainingBytes))
                            .tint(entitlement.remainingBytes == 0 ? Theme.error : Theme.primary).accessibilityHidden(true)
                        Text("剩余 \(Formatting.bytes(entitlement.remainingBytes)) / \(Formatting.bytes(entitlement.totalBytes))")
                            .font(.subheadline.monospacedDigit()).foregroundStyle(Theme.textPrimary)
                            .accessibilityIdentifier("account-remaining")
                        Text("流量以 GiB（2³⁰ 字节）计，按线路倍率扣减。").font(.footnote).foregroundStyle(Theme.textSecondary)
                    }
                    if !entitlement.accountAuthorized, !entitlement.reasonMessage.isEmpty {
                        Text(entitlement.reasonMessage).font(.subheadline).foregroundStyle(Theme.error).accessibilityIdentifier("account-reason")
                    }
                } else {
                    Text("暂无有效套餐").font(.headline).foregroundStyle(Theme.textPrimary).accessibilityIdentifier("account-plan")
                    Text("请到官网管理套餐").font(.subheadline).foregroundStyle(Theme.textSecondary)
                }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("account-plan-card")
        .padding(.vertical, 4)
    }

    private var about: some View {
        Section {
            LabeledContent("应用版本", value: (try? model.config.get().appVersion) ?? "").accessibilityIdentifier("about-version")
            if let identity = state.identity {
                LabeledContent("Core 版本", value: identity.coreVersion).accessibilityIdentifier("about-core")
                LabeledContent("Mihomo", value: String(identity.mihomoRevision.prefix(12))).accessibilityIdentifier("about-mihomo")
            }
            Button("开源许可与源代码") { showLicenses = true }.accessibilityIdentifier("open-licenses")
            Button(copied ? "已复制" : "复制诊断信息") { copyDiagnostics() }.accessibilityIdentifier("copy-diagnostics")
        } header: { SectionTitle(text: "关于") }
    }

    private var vpnText: String {
        switch vpnInstalled {
        case .some(true): "已添加"
        case .some(false): "尚未添加"
        case nil: "—"
        }
    }

    private func loadPermissions() async {
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        notifications = switch settings.authorizationStatus {
        case .authorized, .provisional, .ephemeral: "已允许"
        case .denied: "已拒绝"
        default: "尚未请求"
        }
        try? await vpn.load()
        vpnInstalled = vpn.manager != nil
    }

    private func copyDiagnostics() {
        let channel = (try? model.config.get().environment.channel) ?? .release
        UIPasteboard.general.string = Diagnostics.report(appVersion: (try? model.config.get().appVersion) ?? "", channel: channel, state: state)
        copied = true
    }
}

/// What ships inside the app and where its source is (Mihomo is GPL-3.0).
struct LicensesView: View {
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section {
                    entry("Mihomo (Clash.Meta)", "GPL-3.0", "https://github.com/MetaCubeX/mihomo")
                    entry("sing-tun 及其依赖", "GPL-3.0 / MIT 等", "https://github.com/SagerNet/sing-tun")
                    entry("AsterLink", "本应用源代码", "https://github.com/StevenWinsir/vpn")
                } footer: {
                    Text("完整的第三方许可清单与对应源码将随正式发行包提供。").font(.footnote)
                }
            }
            .navigationTitle("开源许可")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("完成") { dismiss() } } }
        }
        .accessibilityIdentifier("licenses-sheet")
    }

    private func entry(_ name: String, _ license: String, _ url: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(name).font(.body)
            Text(license).font(.subheadline).foregroundStyle(Theme.textSecondary)
            Text(url).font(.footnote).foregroundStyle(Theme.textSecondary)
        }
        .accessibilityElement(children: .combine)
    }
}

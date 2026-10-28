import AsterLinkShared
import SwiftUI

/// Account, website links, appearance, background running, the background service, about, and the
/// two ways out (sign out, quit).
struct AccountPage: View {
    @Bindable var session: SessionController
    let helper: HelperClient
    let diagnostics: AppDiagnostics
    @AppStorage(AppearancePreference.key) private var appearance = AppearancePreference.system.rawValue
    @State private var confirmSignOut = false
    @State private var copied = false

    private var snap: CoreSnapshot { session.snapshot }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                PageHeader(title: "账户")
                accountCard
                section("外观") {
                    Picker("外观", selection: $appearance) {
                        ForEach(AppearancePreference.allCases) { Text($0.title).tag($0.rawValue) }
                    }
                    .pickerStyle(.segmented).labelsHidden().fixedSize()
                    .onChange(of: appearance) { _, value in AppearancePreference(rawValue: value)?.apply() }
                }
                section("通用") {
                    Label {
                        Text("关闭窗口后，AsterLink 会继续在菜单栏运行，连接不会中断。点击菜单栏中的 AsterLink 图标即可重新打开窗口。")
                            .font(.system(size: 13)).foregroundStyle(Theme.textSecondary).fixedSize(horizontal: false, vertical: true)
                    } icon: {
                        Image(systemName: "menubar.rectangle").foregroundStyle(Theme.brand)
                    }
                }
                section("后台服务") { BackgroundServiceSection(session: session, helper: helper) }
                section("关于") { about }
                HStack(spacing: 12) {
                    Button("退出登录") { confirmSignOut = true }
                        .buttonStyle(SecondaryButtonStyle())
                    Button("退出 AsterLink") { NSApp.terminate(nil) }
                        .buttonStyle(SecondaryButtonStyle())
                    Spacer()
                }
            }
            .padding(Theme.pagePadding)
            .frame(maxWidth: 760, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
        .alert("退出登录？", isPresented: $confirmSignOut) {
            Button("退出登录", role: .destructive) { Task { await session.signOut() } }
            Button("取消", role: .cancel) {}
        } message: {
            Text("退出登录会断开连接，并清除本机保存的密码。")
        }
    }

    private var accountCard: some View {
        Card(padding: 20) {
            VStack(alignment: .leading, spacing: 16) {
                HStack(spacing: 14) {
                    Text(initial).font(.system(size: 20, weight: .semibold)).foregroundStyle(Theme.onBrand)
                        .frame(width: 48, height: 48)
                        .background(Theme.brandFill, in: Circle())
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 3) {
                        HStack(spacing: 8) {
                            Text(snap.user?.email ?? session.email).font(.system(size: 15, weight: .semibold)).foregroundStyle(Theme.textPrimary)
                                .lineLimit(1).truncationMode(.middle)
                            if snap.user?.role == "admin" {
                                Text("管理员账号").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.brand)
                                    .padding(.horizontal, 6).padding(.vertical, 1).background(Theme.brandSoft, in: Capsule())
                            }
                        }
                        Text(planSummary).font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
                    }
                    Spacer(minLength: 0)
                }
                HStack(spacing: 10) {
                    Link(destination: session.plansURL) { Label(snap.entitlement?.accountAuthorized == true ? "续费或升级" : "购买套餐", systemImage: "crown") }
                        .buttonStyle(PrimaryButtonStyle(height: 30))
                    Link(destination: session.websiteBase.appendingPathComponent("orders")) { Label("订单记录", systemImage: "list.bullet.rectangle") }
                        .buttonStyle(SecondaryButtonStyle(height: 30))
                    Link(destination: session.websiteBase.appendingPathComponent("dashboard")) { Label("网站控制台", systemImage: "safari") }
                        .buttonStyle(SecondaryButtonStyle(height: 30))
                }
            }
        }
    }

    private var initial: String {
        String((snap.user?.email ?? session.email).first.map { String($0).uppercased() } ?? "A")
    }

    private var planSummary: String {
        guard let entitlement = snap.entitlement, entitlement.accountAuthorized else { return "暂无有效套餐" }
        let name = entitlement.planName.isEmpty ? "当前套餐" : entitlement.planName
        var parts = [name]
        if entitlement.subscriptionExpiresAt != nil { parts.append("\(Formatting.date(entitlement.subscriptionExpiresAt)) 到期") }
        if entitlement.totalBytes > 0 { parts.append("剩余 \(Formatting.bytes(entitlement.remainingBytes))") }
        return parts.joined(separator: " · ")
    }

    private var about: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 12) {
                BrandMark(size: 36)
                VStack(alignment: .leading, spacing: 2) {
                    Text("AsterLink").font(.system(size: 14, weight: .semibold)).foregroundStyle(Theme.textPrimary)
                    HStack(spacing: 6) {
                        Text("版本 \(diagnostics.appVersion)")
                        if diagnostics.isDevelopment {
                            Text("· 开发版") // copy-guard: D8 neutral marker, development builds only
                        }
                    }
                    .font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
                }
            }
            HStack(spacing: 10) {
                Button(copied ? "已复制" : "复制诊断信息") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(diagnostics.report(for: snap), forType: .string)
                    copied = true
                    DispatchQueue.main.asyncAfter(deadline: .now() + 2) { copied = false }
                }
                .buttonStyle(SecondaryButtonStyle(height: 28))
                .help("联系客服时可粘贴这段信息，其中只有应用版本、系统版本、连接状态和错误码")
                Link("访问官网", destination: session.websiteBase).buttonStyle(SecondaryButtonStyle(height: 28))
            }
        }
    }

    private func section<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            SectionTitle(text: title)
            Card { content().frame(maxWidth: .infinity, alignment: .leading) }
        }
    }
}

/// D11: one line when all is well; otherwise the reason and one button that fixes it. Removal sits in
/// a collapsed troubleshooting area and asks first. Every button calls the existing HelperClient methods
/// under the same conditions as the old settings sheet.
struct BackgroundServiceSection: View {
    let session: SessionController
    let helper: HelperClient
    @State private var troubleshooting = false
    @State private var confirmRemove = false

    private var idle: Bool { HelperUpgradePolicy.mayChangeService(whileState: session.snapshot.state) && !helper.busy }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            status
            if let error = helper.lastError, error != HelperClient.approvalHint {
                NoticeBar(kind: .danger, text: error)
            }
            Button { withAnimation(.easeOut(duration: 0.2)) { troubleshooting.toggle() } } label: {
                HStack(spacing: 6) {
                    Image(systemName: "chevron.right").font(.system(size: 10, weight: .semibold))
                        .rotationEffect(.degrees(troubleshooting ? 90 : 0))
                    Text("故障排查").font(.system(size: 13))
                }
                .foregroundStyle(Theme.textSecondary)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityValue(troubleshooting ? "已展开" : "已收起")
            if troubleshooting {
                VStack(alignment: .leading, spacing: 10) {
                    Text("连接异常且无法恢复时，可以重新安装后台服务。断开连接后才能操作。")
                        .font(.system(size: 12)).foregroundStyle(Theme.textSecondary).fixedSize(horizontal: false, vertical: true)
                    HStack(spacing: 10) {
                        Button("重新安装后台服务") { Task { await helper.reinstall() } }.disabled(!idle)
                        Button("打开系统设置") { helper.openSystemSettings() }
                        Button("移除后台服务", role: .destructive) { confirmRemove = true }
                            .disabled(!idle || helper.registration == .notRegistered)
                    }
                    .buttonStyle(SecondaryButtonStyle(height: 28))
                }
            }
        }
        .alert("移除后台服务？", isPresented: $confirmRemove) {
            Button("移除", role: .destructive) { Task { await helper.unregister() } }
            Button("取消", role: .cancel) {}
        } message: {
            Text("移除后将无法连接，直到再次启用。卸载 AsterLink 前可以先移除。")
        }
    }

    @ViewBuilder private var status: some View {
        switch helper.registration {
        case .enabled:
            if let problem = session.serviceProblem {
                row("exclamationmark.circle.fill", Theme.dangerText, problem) {
                    Button("重新安装") { Task { await helper.reinstall() } }.disabled(!idle)
                }
            } else {
                row("checkmark.circle.fill", Theme.brand, "后台服务运行正常") { EmptyView() }
            }
        case .requiresApproval:
            row("exclamationmark.triangle.fill", Theme.warnText, "需要在系统设置中允许 AsterLink 在后台运行") {
                Button("打开系统设置") { helper.register() }
            }
        default:
            row("exclamationmark.triangle.fill", Theme.warnText, "后台服务未启用，暂时无法连接") {
                Button("启用") { helper.register() }.disabled(helper.registration == .enabled)
            }
        }
    }

    private func row<Action: View>(_ symbol: String, _ color: Color, _ text: String, @ViewBuilder action: () -> Action) -> some View {
        HStack(spacing: 10) {
            Image(systemName: symbol).foregroundStyle(color).accessibilityHidden(true)
            Text(text).font(.system(size: 13)).foregroundStyle(Theme.textPrimary).fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 8)
            action().buttonStyle(PrimaryButtonStyle(height: 28))
        }
    }
}

import AsterLinkShared
import SwiftUI

/// Sign-in: the brand column on the left (the website's sign-in story), the form on the right.
/// The first-run background service guide sits above the form until the service is allowed.
struct LoginView: View {
    @Bindable var session: SessionController
    let helper: HelperClient
    let diagnostics: AppDiagnostics
    @State private var password = ""
    @State private var revealPassword = false
    @FocusState private var focus: Field?

    private enum Field { case email, password }

    var body: some View {
        HStack(spacing: 0) {
            BrandColumn()
                .frame(width: 340)
            ScrollView {
                VStack(spacing: 20) {
                    if helper.registration != .enabled {
                        HelperSetupCard(helper: helper)
                    }
                    form
                    footer
                }
                .frame(maxWidth: 380)
                .padding(.horizontal, 40).padding(.vertical, 48)
                .frame(maxWidth: .infinity, minHeight: 560)
            }
            .background(Theme.background)
        }
        .onAppear { focus = session.email.isEmpty ? .email : .password }
        // A remembered password signs in by itself: drop the focus so the system's password AutoFill
        // bubble does not stay on screen after the form is gone.
        .onDisappear { focus = nil }
    }

    private var form: some View {
        Card(padding: 28) {
            VStack(alignment: .leading, spacing: 16) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("登录 AsterLink").font(.system(size: 22, weight: .semibold)).foregroundStyle(Theme.textPrimary)
                        .accessibilityAddTraits(.isHeader)
                    Text("使用你在官网注册的邮箱和密码登录").font(.system(size: 13)).foregroundStyle(Theme.textSecondary)
                }
                field("邮箱") {
                    TextField("name@example.com", text: $session.email)
                        .textContentType(.username).autocorrectionDisabled()
                        .focused($focus, equals: .email)
                        .onSubmit { focus = .password }
                }
                field("密码") {
                    HStack(spacing: 6) {
                        Group {
                            if revealPassword {
                                TextField("请输入密码", text: $password)
                            } else {
                                SecureField("请输入密码", text: $password)
                            }
                        }
                        .textContentType(.password)
                        .focused($focus, equals: .password)
                        .onSubmit(submit)
                        Button { revealPassword.toggle() } label: {
                            Image(systemName: revealPassword ? "eye.slash" : "eye").foregroundStyle(Theme.textTertiary)
                        }
                        .buttonStyle(.plain)
                        .help(revealPassword ? "隐藏密码" : "显示密码")
                        .accessibilityLabel(revealPassword ? "隐藏密码" : "显示密码")
                    }
                }
                Toggle("记住密码", isOn: $session.rememberPassword).toggleStyle(.checkbox)
                    .font(.system(size: 13)).foregroundStyle(Theme.textSecondary)

                Button(action: submit) {
                    if session.phase == .signingIn {
                        ProgressView().controlSize(.small).tint(Theme.onBrand)
                    } else {
                        Text("登录")
                    }
                }
                .buttonStyle(PrimaryButtonStyle(height: 38, fullWidth: true))
                .keyboardShortcut(.defaultAction)
                .disabled(session.phase == .signingIn || session.email.isEmpty || password.isEmpty || helper.registration != .enabled)

                if helper.registration == .enabled, let problem = session.message ?? session.serviceProblem {
                    NoticeBar(kind: .danger, text: problem)
                }
                if helper.registration == .enabled, session.serviceProblem != nil {
                    ServiceRepairRow(helper: helper, session: session)
                }
                if let warning = session.warning { NoticeBar(kind: .warning, text: warning) }

                HStack(spacing: 0) {
                    Link("注册账号", destination: session.registerURL)
                    Spacer()
                    Link("忘记密码", destination: session.forgotPasswordURL)
                    Spacer()
                    Link("购买套餐", destination: session.plansURL)
                }
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(Theme.brand)
            }
        }
    }

    private func field<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title).font(.system(size: 12, weight: .medium)).foregroundStyle(Theme.textSecondary)
            content()
                .textFieldStyle(.plain)
                .font(.system(size: 14))
                .padding(.horizontal, 10)
                .frame(height: 34)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.controlRadius, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.controlRadius, style: .continuous).strokeBorder(Theme.strokeStrong, lineWidth: 1))
        }
    }

    private var footer: some View {
        HStack(spacing: 6) {
            Text("版本 \(diagnostics.appVersion)")
            if diagnostics.isDevelopment {
                Text("· 开发版") // copy-guard: D8 neutral marker, development builds only
            }
        }
        .font(.system(size: 11)).foregroundStyle(Theme.textTertiary)
    }

    private func submit() {
        guard !(session.phase == .signingIn || session.email.isEmpty || password.isEmpty || helper.registration != .enabled) else { return }
        let secret = password
        focus = nil
        Task {
            await session.signIn(email: session.email, password: secret, remember: session.rememberPassword)
            if session.phase == .signedIn { password = "" }
        }
    }
}

/// The website's dark brand column: mark, the value statement of the home page, three promises.
struct BrandColumn: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Wordmark(size: 30, textColor: Theme.storyText)
            Spacer(minLength: 32)
            VStack(alignment: .leading, spacing: 14) {
                Text("住宅原生 IP · 专线稳定连接").font(.system(size: 12, weight: .medium)).foregroundStyle(Theme.storyCheck)
                Text("住宅原生 IP\n专线加持，稳定在线").font(.system(size: 26, weight: .semibold)).foregroundStyle(Theme.storyText)
                    .lineSpacing(4).fixedSize(horizontal: false, vertical: true)
                Text("一个账户，手机与电脑随时安全连接；线路、流量与订阅一目了然。")
                    .font(.system(size: 13)).foregroundStyle(Theme.storyCopy).lineSpacing(3)
                    .fixedSize(horizontal: false, vertical: true)
                VStack(alignment: .leading, spacing: 10) {
                    promise("全平台客户端，一个账户通用")
                    promise("线路倍率公开，计费清晰透明")
                    promise("邮箱验证与登录记录，账户更安全")
                }
                .padding(.top, 10)
            }
            Spacer(minLength: 32)
            Text("© \(String(Calendar.current.component(.year, from: Date()))) AsterLink")
                .font(.system(size: 11)).foregroundStyle(Theme.storyCopy)
        }
        .padding(.horizontal, 32).padding(.top, 44).padding(.bottom, 28)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
        .background(Theme.storyBackground)
    }

    private func promise(_ text: String) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "checkmark").font(.system(size: 12, weight: .bold)).foregroundStyle(Theme.storyCheck)
                .accessibilityHidden(true)
            Text(text).font(.system(size: 13)).foregroundStyle(Theme.storyText)
        }
    }
}

/// Shown when the background service is enabled but cannot be reached (for example after an update
/// left an older copy running): the same reinstall the account page offers.
struct ServiceRepairRow: View {
    let helper: HelperClient
    let session: SessionController

    var body: some View {
        HStack(spacing: 10) {
            Text("仍无法使用？可以重新安装后台服务。").font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
            Spacer(minLength: 8)
            Button("重新安装") { Task { await helper.reinstall() } }
                .buttonStyle(SecondaryButtonStyle(height: 28))
                .disabled(!HelperUpgradePolicy.mayChangeService(whileState: session.snapshot.state) || helper.busy)
        }
    }
}

/// macOS asks the user to allow a new background service once (any signature, Developer ID
/// included); the app cannot do it for them. Three steps: 启用 registers the service and opens the
/// exact System Settings page, the user turns on one switch, and the app notices the approval by
/// itself (HelperClient.watchRegistration) and carries on.
struct HelperSetupCard: View {
    let helper: HelperClient

    var body: some View {
        Card(padding: 20) {
            VStack(alignment: .leading, spacing: 14) {
                HStack(spacing: 10) {
                    Image(systemName: "lock.shield.fill").font(.system(size: 20)).foregroundStyle(Theme.brand)
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("首次使用：允许 AsterLink 在后台运行").font(.system(size: 15, weight: .semibold))
                            .foregroundStyle(Theme.textPrimary).accessibilityAddTraits(.isHeader)
                        Text("建立安全连接需要一项后台服务，只需允许一次。").font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
                    }
                }
                if let problem = helper.installProblem {
                    NoticeBar(kind: .warning, text: problem)
                } else {
                    VStack(alignment: .leading, spacing: 10) {
                        step(1, "点击下方“启用”", done: waiting)
                        step(2, "在自动打开的“系统设置 › 登录项与扩展”中，打开“允许在后台”里 AsterLink 的开关（可能需要输入本机密码或使用触控 ID）", done: false)
                        step(3, "回到这里，AsterLink 会自动继续，无需其他操作", done: false)
                    }
                    HStack(spacing: 10) {
                        Button(waiting ? "再次打开系统设置" : "启用") { helper.register() }
                            .buttonStyle(PrimaryButtonStyle())
                        if waiting {
                            ProgressView().controlSize(.small)
                            Text("等待你在系统设置中允许…").font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
                        }
                    }
                    if let error = helper.lastError, error != HelperClient.approvalHint {
                        NoticeBar(kind: .danger, text: error)
                    }
                }
            }
        }
    }

    private var waiting: Bool { helper.registration == .requiresApproval }

    private func step(_ n: Int, _ text: String, done: Bool) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Image(systemName: done ? "checkmark.circle.fill" : "\(n).circle")
                .foregroundStyle(done ? Theme.brand : Theme.textTertiary)
                .accessibilityLabel(done ? "已完成" : "第 \(n) 步")
            Text(text).font(.system(size: 13)).foregroundStyle(Theme.textPrimary).fixedSize(horizontal: false, vertical: true)
        }
    }
}

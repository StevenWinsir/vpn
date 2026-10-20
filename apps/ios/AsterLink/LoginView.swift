import AsterLinkKit
import SwiftUI

/// The signed-out page (handoff_app_ios.md 10.3): brand, email and password (the system
/// Passwords app can fill them), remember-password, one wide button, and the website's register page.
struct LoginView: View {
    @Environment(AppModel.self) private var model
    let session: SessionController

    @State private var email = ""
    @State private var password = ""
    @State private var remember = false
    @State private var showPassword = false
    @State private var seeded = false
    @FocusState private var focus: Field?

    private enum Field { case email, password }

    private var state: SessionState { session.state }
    private var signingIn: Bool { state.phase == .signingIn }

    /// Codes that belong to one field; everything else is a banner at the top.
    private static let fieldCodes: Set<String> = [LocalCodes.inputMissing, LocalCodes.emailTooLong, LocalCodes.passwordTooLong, "invalid_credentials"]
    private var emailEmpty: Bool { email.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    private var emailProblem: String? {
        guard let notice = state.message else { return nil }
        if notice.code == LocalCodes.emailTooLong || (notice.code == LocalCodes.inputMissing && emailEmpty) { return ErrorText.text(for: notice) }
        return nil
    }

    private var passwordProblem: String? {
        guard let notice = state.message else { return nil }
        if [LocalCodes.passwordTooLong, "invalid_credentials"].contains(notice.code) || (notice.code == LocalCodes.inputMissing && !emailEmpty) {
            return ErrorText.text(for: notice)
        }
        return nil
    }

    private var banner: Notice? {
        if let notice = state.coreProblem { return notice }
        guard let notice = state.message, !Self.fieldCodes.contains(notice.code) else { return nil }
        return notice
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 24) {
                header
                if let banner { Banner(kind: .error, text: ErrorText.text(for: banner), identifier: "login-banner") }
                if let warning = state.warning { Banner(kind: .warning, text: ErrorText.text(for: warning), identifier: "login-warning") }
                form
                Button(action: submit) {
                    if signingIn { HStack(spacing: 8) { ProgressView().tint(.white); Text("正在登录…") } } else { Text("登录") }
                }
                .buttonStyle(PrimaryButtonStyle())
                .disabled(signingIn)
                .accessibilityIdentifier("login-submit")
                links
                footer
            }
            .padding(.horizontal, Theme.pageMargin)
            .padding(.vertical, 32)
            .frame(maxWidth: 520)
            .frame(maxWidth: .infinity)
        }
        .scrollDismissesKeyboard(.interactively)
        .background(Theme.background.ignoresSafeArea())
        .onAppear(perform: seed)
        .onChange(of: state.email) { _, value in if email.isEmpty { email = value } }
        .onChange(of: state.rememberPassword) { _, value in if value { remember = true } }
    }

    private func seed() {
        guard !seeded else { return }
        seeded = true
        email = state.email
        remember = state.rememberPassword
    }

    private var header: some View {
        VStack(spacing: 10) {
            BrandMark(size: 72)
            Text("登录 AsterLink").font(.title.weight(.semibold)).foregroundStyle(Theme.textPrimary)
            Text("使用官网注册的账号登录").font(.subheadline).foregroundStyle(Theme.textSecondary)
        }
        .padding(.top, 24)
        .accessibilityElement(children: .combine)
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: 16) {
            field("邮箱", problem: emailProblem, problemID: "login-email-error") {
                TextField("name@example.com", text: $email)
                    .keyboardType(.emailAddress).textContentType(.username)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .submitLabel(.next).focused($focus, equals: .email)
                    .onSubmit { focus = .password }
                    .accessibilityIdentifier("login-email")
            }
            field("密码", problem: passwordProblem, problemID: "login-password-error") {
                HStack {
                    Group {
                        if showPassword { TextField("密码", text: $password) } else { SecureField("密码", text: $password) }
                    }
                    .textContentType(.password).textInputAutocapitalization(.never).autocorrectionDisabled()
                    .submitLabel(.go).focused($focus, equals: .password)
                    .onSubmit(submit)
                    .accessibilityIdentifier("login-password")
                    Button { showPassword.toggle() } label: {
                        Image(systemName: showPassword ? "eye.slash" : "eye").frame(width: 44, height: 44)
                    }
                    .foregroundStyle(Theme.textSecondary)
                    .accessibilityLabel(showPassword ? "隐藏密码" : "显示密码")
                    .accessibilityIdentifier("login-show-password")
                }
            }
            Button { remember.toggle() } label: {
                HStack(spacing: 12) {
                    Image(systemName: remember ? "checkmark.square.fill" : "square")
                        .font(.title3).foregroundStyle(remember ? Theme.primary : Theme.textSecondary)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("记住密码").font(.body).foregroundStyle(Theme.textPrimary)
                        Text("密码保存在本机钥匙串中，不会同步到 iCloud").font(.footnote).foregroundStyle(Theme.textSecondary)
                    }
                    Spacer(minLength: 0)
                }
                .frame(minHeight: 44)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel("记住密码")
            .accessibilityValue(remember ? "已开启" : "已关闭")
            .accessibilityAddTraits(.isToggle)
            .accessibilityIdentifier("login-remember")
        }
    }

    private func field<Content: View>(_ title: String, problem: String?, problemID: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title).font(.footnote.weight(.semibold)).foregroundStyle(Theme.textSecondary)
            content()
                .padding(.horizontal, 14).frame(minHeight: 48)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).stroke(problem == nil ? Theme.stroke : Theme.error, lineWidth: 1))
            if let problem {
                Label(problem, systemImage: "exclamationmark.circle.fill")
                    .font(.footnote).foregroundStyle(Theme.error)
                    .accessibilityIdentifier(problemID)
            }
        }
    }

    private var links: some View {
        // Both flows run on the website (verification code, reset link); the app only opens it.
        HStack(spacing: 24) {
            Button("注册账号") { model.openWebsite("register", title: "注册账号") }
                .accessibilityIdentifier("login-register")
            Button("忘记密码") { model.openWebsite("forgot-password", title: "忘记密码") }
                .accessibilityIdentifier("login-forgot-password")
        }
        .font(.subheadline.weight(.semibold))
        .frame(minHeight: 44)
    }

    private var footer: some View {
        VStack(spacing: 2) {
            if let identity = state.identity {
                Text("版本 \(appVersion) · Core \(identity.coreVersion) · Mihomo \(String(identity.mihomoRevision.prefix(7)))")
            } else {
                Text("版本 \(appVersion)")
            }
            if case .success(let config) = model.config, config.environment.channel == .development {
                Text("开发版 · \(config.environment.apiBase.host() ?? "")")
            }
        }
        .font(.footnote.monospacedDigit()).foregroundStyle(Theme.textSecondary)
        .multilineTextAlignment(.center)
        .accessibilityIdentifier("login-footer")
    }

    private var appVersion: String { (try? model.config.get().appVersion) ?? "" }

    private func submit() {
        guard !signingIn else { return }
        focus = nil
        let email = email, password = password, remember = remember
        Task { await session.signIn(email: email, password: password, remember: remember) }
    }
}

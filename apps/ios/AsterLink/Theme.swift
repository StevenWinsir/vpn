import AsterLinkKit
import SwiftUI

/// Design tokens shared with the Windows and Android clients (handoff_app_ios.md 10.2).
enum Theme {
    static let primary = Color(light: 0x2F6FEB, dark: 0x5B8DEF)
    static let connected = Color(light: 0x16A34A, dark: 0x22C55E)
    static let pending = Color(light: 0xD97706, dark: 0xF59E0B)
    static let error = Color(light: 0xDC2626, dark: 0xF87171)
    static let background = Color(light: 0xF6F8FB, dark: 0x0B1220)
    static let surface = Color(light: 0xFFFFFF, dark: 0x121A2B)
    static let textPrimary = Color(light: 0x0F172A, dark: 0xE6EAF2)
    static let textSecondary = Color(light: 0x5B6475, dark: 0x98A2B3)
    static let stroke = Color(light: 0xE3E8EF, dark: 0x243049)
    static let cornerRadius: CGFloat = 16
    static let pageMargin: CGFloat = 20

    static func color(for tone: Tone) -> Color {
        switch tone {
        case .idle: textSecondary
        case .busy: pending
        case .good: connected
        case .bad: error
        }
    }
}

extension Color {
    init(light: UInt32, dark: UInt32) {
        self.init(uiColor: UIColor { traits in
            let hex = traits.userInterfaceStyle == .dark ? dark : light
            return UIColor(red: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255,
                           blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
        })
    }
}

/// A rounded, hairline-stroked surface (no shadow).
struct Card<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        content
            .padding(16)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous).stroke(Theme.stroke, lineWidth: 1))
    }
}

/// The 52 pt capsule used for the one primary action on a page.
struct PrimaryButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.headline)
            .foregroundStyle(.white)
            .frame(maxWidth: .infinity, minHeight: 52)
            .background(Capsule().fill(isEnabled ? Theme.primary : Theme.textSecondary.opacity(0.45)))
            .opacity(configuration.isPressed ? 0.85 : 1)
            .contentShape(Capsule())
    }
}

/// A bordered capsule for secondary actions.
struct SecondaryButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(Theme.primary)
            .frame(maxWidth: .infinity, minHeight: 44)
            .overlay(Capsule().stroke(Theme.primary.opacity(0.55), lineWidth: 1))
            .opacity(configuration.isPressed ? 0.7 : 1)
            .contentShape(Capsule())
    }
}

/// A one-line explanation with an icon. Colour never carries the meaning alone: the icon and the
/// words say it too.
struct Banner: View {
    enum Kind { case error, warning, info }

    let kind: Kind
    let text: String
    var identifier = "banner"
    var dismiss: (() -> Void)?

    private var tint: Color { kind == .error ? Theme.error : kind == .warning ? Theme.pending : Theme.primary }
    private var symbol: String {
        kind == .error ? "exclamationmark.octagon.fill" : kind == .warning ? "exclamationmark.triangle.fill" : "info.circle.fill"
    }

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: symbol).foregroundStyle(tint).accessibilityHidden(true)
            Text(text)
                .font(.subheadline)
                .foregroundStyle(Theme.textPrimary)
                .frame(maxWidth: .infinity, alignment: .leading)
                .accessibilityIdentifier(identifier)
            if let dismiss {
                Button(action: dismiss) { Image(systemName: "xmark").font(.footnote.weight(.bold)).frame(width: 44, height: 44) }
                    .foregroundStyle(Theme.textSecondary)
                    .accessibilityLabel("关闭提示")
            }
        }
        .padding(.leading, 14).padding(.vertical, dismiss == nil ? 12 : 0).padding(.trailing, dismiss == nil ? 14 : 0)
        .background(tint.opacity(0.12), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .accessibilityElement(children: .contain)
    }
}

/// Header of a page whose big title is part of the content (inside a scroll view).
struct SectionTitle: View {
    let text: String

    var body: some View {
        Text(text).font(.footnote.weight(.semibold)).foregroundStyle(Theme.textSecondary).textCase(nil)
    }
}

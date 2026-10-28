import AsterLinkKit
import SwiftUI

/// Design tokens: one role per website token (frontend/src/app/globals.css), light and dark, checked
/// against it by scripts/test-brand-assets.py. Views only use the role names.
enum Theme {
    // Brand: text and icons use `brand`; filled surfaces (buttons, the connected disc) use `brandFill`.
    static let brand = Color(light: 0x0D7B61, dark: 0x3DC89A)
    static let brandFill = Color(light: 0x0D7B61, dark: 0x0D7B61)
    static let onBrand = Color(light: 0xFFFFFF, dark: 0xFFFFFF)
    static let brandSoft = Color(light: 0xE8F5EF, dark: 0x3DC89A, darkAlpha: 0.13)
    // Surfaces and borders.
    static let background = Color(light: 0xF7F8FA, dark: 0x121A18)
    static let surface = Color(light: 0xFFFFFF, dark: 0x19221F)
    static let surfaceMuted = Color(light: 0xF3F7F5, dark: 0x212C29)
    static let stroke = Color(light: 0xE9EEED, dark: 0x2A3633)
    static let strokeStrong = Color(light: 0xCDD9D4, dark: 0x3D4D49)
    // Text.
    static let textPrimary = Color(light: 0x172D2B, dark: 0xDBE5E2)
    static let textSecondary = Color(light: 0x566A66, dark: 0xB4C3BF)
    static let textTertiary = Color(light: 0x66756F, dark: 0x93A5A0)
    // Warning and danger: text, background and border come as a set.
    static let warnText = Color(light: 0x7C2D12, dark: 0xFDBA74)
    static let warnBackground = Color(light: 0xFFF4EC, dark: 0xEA580C, darkAlpha: 0.12)
    static let warnBorder = Color(light: 0xF3C7A8, dark: 0xFB923C, darkAlpha: 0.4)
    static let dangerText = Color(light: 0xB91C1C, dark: 0xFF8787)
    static let dangerBackground = Color(light: 0xFDECEC, dark: 0xFA5252, darkAlpha: 0.12)
    static let focus = Color(light: 0x3DC89A, dark: 0x3DC89A)

    // Names the views use for state. Connected is the brand green; a state is never colour alone.
    static let primary = brand
    static let connected = brand
    static let pending = warnText
    static let error = dangerText
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
    init(light: UInt32, dark: UInt32, lightAlpha: CGFloat = 1, darkAlpha: CGFloat = 1) {
        self.init(uiColor: UIColor { traits in
            let isDark = traits.userInterfaceStyle == .dark
            let hex = isDark ? dark : light
            return UIColor(red: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255,
                           blue: CGFloat(hex & 0xFF) / 255, alpha: isDark ? darkAlpha : lightAlpha)
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
            .foregroundStyle(Theme.onBrand)
            .frame(maxWidth: .infinity, minHeight: 52)
            .background(Capsule().fill(isEnabled ? Theme.brandFill : Theme.textSecondary.opacity(0.45)))
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

    private var tint: Color { kind == .error ? Theme.dangerText : kind == .warning ? Theme.warnText : Theme.brand }
    private var fill: Color { kind == .error ? Theme.dangerBackground : kind == .warning ? Theme.warnBackground : Theme.brandSoft }
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
        .background(fill, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
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

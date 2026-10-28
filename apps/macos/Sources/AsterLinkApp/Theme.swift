import AppKit
import SwiftUI

/// Design tokens: one role per website token (frontend/src/app/globals.css), light and dark, checked
/// against it by scripts/test-brand-assets.py. Views only use the role names, never a colour literal.
enum Theme {
    // Brand: text and icons use `brand`; filled surfaces (buttons, the connected disc) use `brandFill`.
    static let brand = Color(light: 0x0D7B61, dark: 0x3DC89A)
    static let brandFill = Color(light: 0x0D7B61, dark: 0x0D7B61)
    static let brandFillHover = Color(light: 0x09624F, dark: 0x09624F)
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
    // The rate chart.
    static let chartGrid = Color(light: 0xEEF3F0, dark: 0x24302D)
    // The brand column of the sign-in window (the website's dark story column, both schemes).
    static let storyBackground = Color(light: 0x123F34, dark: 0x0F2F28)
    static let storyText = Color(light: 0xFFFFFF, dark: 0xFFFFFF)
    static let storyCopy = Color(light: 0xB1C9BE, dark: 0xB1C9BE)
    static let storyCheck = Color(light: 0x77C6A4, dark: 0x77C6A4)
    // Light scheme cards float on a faint shadow; dark ones are separated by their border only.
    static let cardShadow = Color(light: 0x172D2B, dark: 0x000000, lightAlpha: 0.06, darkAlpha: 0)

    // Names the views use for state. Connected is the brand green; a state is never colour alone.
    static let connected = brand
    static let pending = warnText
    static let error = dangerText

    // Geometry (handoff 5.1: 8 pt grid, cards 12, controls 8).
    static let cardRadius: CGFloat = 12
    static let controlRadius: CGFloat = 8
    static let pagePadding: CGFloat = 28

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
    /// A colour that follows the appearance of the view drawing it (the window, or the theme override).
    init(light: UInt32, dark: UInt32, lightAlpha: CGFloat = 1, darkAlpha: CGFloat = 1) {
        self.init(nsColor: NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            let hex = isDark ? dark : light
            return NSColor(srgbRed: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255,
                           blue: CGFloat(hex & 0xFF) / 255, alpha: isDark ? darkAlpha : lightAlpha)
        })
    }
}

/// The appearance preference (D14): follow the system, or force light or dark. Stored on this Mac only.
enum AppearancePreference: String, CaseIterable, Identifiable {
    case system, light, dark

    static let key = "appearance"
    var id: String { rawValue }

    var title: String {
        switch self {
        case .system: "跟随系统"
        case .light: "浅色"
        case .dark: "深色"
        }
    }

    static var current: AppearancePreference {
        AppearancePreference(rawValue: UserDefaults.standard.string(forKey: key) ?? "") ?? .system
    }

    /// Applied app-wide, so the main window and the menu bar panel always agree.
    func apply() {
        switch self {
        case .system: NSApp.appearance = nil
        case .light: NSApp.appearance = NSAppearance(named: .aqua)
        case .dark: NSApp.appearance = NSAppearance(named: .darkAqua)
        }
    }
}

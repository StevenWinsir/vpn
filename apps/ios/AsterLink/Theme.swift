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

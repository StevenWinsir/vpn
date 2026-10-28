import AsterLinkShared
import Charts
import SwiftUI

// MARK: - Brand

/// The website's mark (frontend/brand/mark.svg), drawn from the same path data so it stays sharp at
/// every size. scripts/test-brand-assets.py checks these strings against the SVG file.
enum BrandGeometry {
    static let viewBox: CGFloat = 64
    static let cornerRadius: CGFloat = 15
    static let strokeWidth: CGFloat = 4.2
    static let shieldPath = "M32 14a24 24 0 0 0 17 6a24 24 0 0 1 -17 30a24 24 0 0 1 -17 -30a24 24 0 0 0 17 -6"
    static let stemPath = "M32 32v5"
    static let dot = (x: CGFloat(32), y: CGFloat(30), r: CGFloat(2.8))
}

/// The shield and its keyhole, without the plate: the glyph of the mark and of the connect button.
struct ShieldGlyph: View {
    var color: Color = .white

    var body: some View {
        GeometryReader { proxy in
            let scale = min(proxy.size.width, proxy.size.height) / BrandGeometry.viewBox
            ZStack {
                SVGPathShape(data: BrandGeometry.shieldPath + " " + BrandGeometry.stemPath)
                    .stroke(color, style: StrokeStyle(lineWidth: BrandGeometry.strokeWidth * scale, lineCap: .round, lineJoin: .round))
                SVGPathShape(data: dotPath).fill(color)
            }
        }
        .aspectRatio(1, contentMode: .fit)
        .accessibilityHidden(true)
    }

    private var dotPath: String {
        let d = BrandGeometry.dot
        return "M\(d.x - d.r) \(d.y)a\(d.r) \(d.r) 0 1 0 \(2 * d.r) 0a\(d.r) \(d.r) 0 1 0 \(-2 * d.r) 0"
    }
}

/// The full mark: brand plate plus white shield.
struct BrandMark: View {
    var size: CGFloat

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: size * BrandGeometry.cornerRadius / BrandGeometry.viewBox, style: .continuous)
                .fill(Theme.brandFill)
            ShieldGlyph(color: Theme.onBrand)
        }
        .frame(width: size, height: size)
        .accessibilityHidden(true)
    }
}

/// Mark and word mark side by side, spaced like the website header.
struct Wordmark: View {
    var size: CGFloat = 26
    var textColor: Color = Theme.textPrimary

    var body: some View {
        HStack(spacing: size * 0.4) {
            BrandMark(size: size)
            Text("AsterLink").font(.system(size: size * 0.66, weight: .semibold)).foregroundStyle(textColor)
                .lineLimit(1).fixedSize()
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("AsterLink")
    }
}

/// A shape from SVG path data in the mark's 64-unit box (M, L, H, V, A and Z, absolute or relative).
struct SVGPathShape: Shape {
    let data: String

    func path(in rect: CGRect) -> Path {
        let scale = min(rect.width, rect.height) / BrandGeometry.viewBox
        let origin = CGPoint(x: rect.midX - BrandGeometry.viewBox * scale / 2, y: rect.midY - BrandGeometry.viewBox * scale / 2)
        return Self.parse(data).applying(CGAffineTransform(translationX: origin.x, y: origin.y).scaledBy(x: scale, y: scale))
    }

    static func parse(_ data: String) -> Path {
        var tokens: [String] = []
        var current = ""
        for char in data {
            if char.isLetter {
                if !current.isEmpty { tokens.append(current); current = "" }
                tokens.append(String(char))
            } else if char == " " || char == "," {
                if !current.isEmpty { tokens.append(current); current = "" }
            } else if char == "-", !current.isEmpty, current.last != "e" {
                tokens.append(current); current = "-"
            } else {
                current.append(char)
            }
        }
        if !current.isEmpty { tokens.append(current) }

        var path = Path()
        var point = CGPoint.zero, start = CGPoint.zero
        var index = 0, command = "M"
        func number() -> CGFloat { defer { index += 1 }; return CGFloat(Double(tokens[index]) ?? 0) }
        while index < tokens.count {
            if let first = tokens[index].first, first.isLetter { command = tokens[index]; index += 1 }
            let relative = command == command.lowercased()
            let base = relative ? point : .zero
            switch command.uppercased() {
            case "M":
                point = CGPoint(x: base.x + number(), y: base.y + number())
                start = point
                path.move(to: point)
                command = relative ? "l" : "L"
            case "L":
                point = CGPoint(x: base.x + number(), y: base.y + number())
                path.addLine(to: point)
            case "H":
                point.x = (relative ? point.x : 0) + number()
                path.addLine(to: point)
            case "V":
                point.y = (relative ? point.y : 0) + number()
                path.addLine(to: point)
            case "A":
                let rx = number(), ry = number()
                _ = number()  // the mark only uses circles: rotation has no effect
                let large = number() != 0, sweep = number() != 0
                let end = CGPoint(x: base.x + number(), y: base.y + number())
                addArc(&path, from: point, to: end, radius: max(rx, ry), large: large, sweep: sweep)
                point = end
            case "Z":
                path.closeSubpath()
                point = start
            default:
                index += 1
            }
        }
        return path
    }

    /// SVG endpoint arc to centre form (SVG 1.1 F.6.5) for a circle. Angles are measured in the
    /// drawing's own y-down space, exactly as SVG measures them, so no flip is involved.
    private static func addArc(_ path: inout Path, from p1: CGPoint, to p2: CGPoint, radius: CGFloat, large: Bool, sweep: Bool) {
        let dx = (p1.x - p2.x) / 2, dy = (p1.y - p2.y) / 2
        var r = radius
        let lambda = (dx * dx + dy * dy) / (r * r)
        if lambda > 1 { r *= sqrt(lambda) }
        let factor = (large != sweep ? 1 : -1) * sqrt(max(0, (r * r - dx * dx - dy * dy) / (dx * dx + dy * dy)))
        let cxp = factor * dy, cyp = -factor * dx
        let center = CGPoint(x: cxp + (p1.x + p2.x) / 2, y: cyp + (p1.y + p2.y) / 2)
        let start = atan2((dy - cyp) / r, (dx - cxp) / r)
        var delta = atan2((-dy - cyp) / r, (-dx - cxp) / r) - start
        if sweep, delta < 0 { delta += 2 * .pi }
        if !sweep, delta > 0 { delta -= 2 * .pi }
        path.addRelativeArc(center: center, radius: r, startAngle: .radians(start), delta: .radians(delta))
    }
}

// MARK: - Surfaces

/// A rounded card on the surface colour with a hairline border (and a faint shadow in light mode).
struct Card<Content: View>: View {
    var padding: CGFloat = 16
    @ViewBuilder var content: Content

    var body: some View {
        content
            .padding(padding)
            .frame(maxWidth: .infinity, alignment: .topLeading)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous).strokeBorder(Theme.stroke, lineWidth: 1))
            .shadow(color: Theme.cardShadow, radius: 12, x: 0, y: 4)
    }
}

/// A page title row: 24 pt title, optional trailing controls.
struct PageHeader<Trailing: View>: View {
    let title: String
    var subtitle: String?
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.system(size: 24, weight: .semibold)).foregroundStyle(Theme.textPrimary)
                    .accessibilityAddTraits(.isHeader)
                if let subtitle {
                    Text(subtitle).font(.system(size: 13)).foregroundStyle(Theme.textSecondary)
                }
            }
            Spacer(minLength: 12)
            trailing
        }
    }
}

extension PageHeader where Trailing == EmptyView {
    init(title: String, subtitle: String? = nil) {
        self.init(title: title, subtitle: subtitle) { EmptyView() }
    }
}

/// A section title inside a page (15 pt).
struct SectionTitle: View {
    let text: String
    var body: some View {
        Text(text).font(.system(size: 15, weight: .semibold)).foregroundStyle(Theme.textPrimary)
            .accessibilityAddTraits(.isHeader)
    }
}

// MARK: - Buttons

/// The one filled action of a view: brand fill, white text, 8 pt corners, hover darkening.
struct PrimaryButtonStyle: ButtonStyle {
    var height: CGFloat = 36
    var fullWidth = false

    func makeBody(configuration: Configuration) -> some View {
        StyledButton(configuration: configuration, primary: true, height: height, fullWidth: fullWidth)
    }
}

/// A quiet bordered action on the surface colour.
struct SecondaryButtonStyle: ButtonStyle {
    var height: CGFloat = 32
    var fullWidth = false

    func makeBody(configuration: Configuration) -> some View {
        StyledButton(configuration: configuration, primary: false, height: height, fullWidth: fullWidth)
    }
}

private struct StyledButton: View {
    let configuration: ButtonStyleConfiguration
    let primary: Bool
    let height: CGFloat
    let fullWidth: Bool
    @Environment(\.isEnabled) private var isEnabled
    @State private var hovering = false

    var body: some View {
        configuration.label
            .font(.system(size: 13, weight: primary ? .semibold : .medium))
            .foregroundStyle(primary ? Theme.onBrand : Theme.textPrimary)
            .padding(.horizontal, 16)
            .frame(maxWidth: fullWidth ? .infinity : nil, minHeight: height)
            .background(background, in: RoundedRectangle(cornerRadius: Theme.controlRadius, style: .continuous))
            .overlay {
                if !primary {
                    RoundedRectangle(cornerRadius: Theme.controlRadius, style: .continuous).strokeBorder(Theme.strokeStrong, lineWidth: 1)
                }
            }
            .opacity(isEnabled ? (configuration.isPressed ? 0.85 : 1) : 0.45)
            .contentShape(RoundedRectangle(cornerRadius: Theme.controlRadius, style: .continuous))
            .onHover { hovering = $0 }
            .animation(.easeOut(duration: 0.15), value: hovering)
    }

    private var background: Color {
        if primary { return hovering && isEnabled ? Theme.brandFillHover : Theme.brandFill }
        return hovering && isEnabled ? Theme.surfaceMuted : Theme.surface
    }
}

// MARK: - Status and notices

/// A coloured dot plus words: a state is never told by colour alone.
struct StatusPill: View {
    let status: ConnectionStatus

    var body: some View {
        HStack(spacing: 6) {
            Circle().fill(Theme.color(for: status.tone)).frame(width: 8, height: 8)
            Text(status.title).font(.system(size: 12, weight: .medium)).foregroundStyle(Theme.textPrimary)
        }
        .padding(.horizontal, 10).padding(.vertical, 5)
        .background(Theme.surface, in: Capsule())
        .overlay(Capsule().strokeBorder(Theme.stroke, lineWidth: 1))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("连接状态：\(status.title)")
    }
}

/// One message with at most one next step, on the warning or danger colours of the website.
struct NoticeBar: View {
    enum Kind { case warning, danger }
    let kind: Kind
    let text: String
    var actionTitle: String?
    var action: (() -> Void)?

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            Image(systemName: kind == .danger ? "exclamationmark.circle.fill" : "exclamationmark.triangle.fill")
                .foregroundStyle(foreground).accessibilityHidden(true)
            Text(text).font(.system(size: 13)).foregroundStyle(foreground)
                .fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
            Spacer(minLength: 8)
            if let actionTitle, let action {
                Button(actionTitle, action: action).buttonStyle(SecondaryButtonStyle(height: 28))
            }
        }
        .padding(.horizontal, 14).padding(.vertical, 10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(kind == .danger ? Theme.dangerBackground : Theme.warnBackground,
                    in: RoundedRectangle(cornerRadius: Theme.controlRadius, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.controlRadius, style: .continuous)
            .strokeBorder(kind == .danger ? Theme.dangerText.opacity(0.25) : Theme.warnBorder, lineWidth: 1))
        .accessibilityElement(children: .contain)
    }

    private var foreground: Color { kind == .danger ? Theme.dangerText : Theme.warnText }
}

/// "专线" or "普通线路" as a small tag; dedicated lines use the brand tint.
struct LineTag: View {
    let node: CoreNode

    var body: some View {
        Text(node.lineLabel)
            .font(.system(size: 11, weight: .semibold))
            .foregroundStyle(node.isDedicated ? Theme.brand : Theme.textSecondary)
            .padding(.horizontal, 7).padding(.vertical, 2)
            .background(node.isDedicated ? Theme.brandSoft : Theme.surfaceMuted, in: Capsule())
    }
}

// MARK: - Connect button

/// The centre of the connect page: neutral ring when idle, a turning brand arc while working,
/// a solid brand disc with a soft glow when connected, a danger ring when the last try failed.
struct ConnectButton: View {
    let status: ConnectionStatus
    let action: PrimaryAction
    var diameter: CGFloat = 156
    let perform: () -> Void
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var turning = false
    @State private var hovering = false

    var body: some View {
        Button(action: perform) {
            ZStack {
                if status == .connected {
                    // A soft halo, drawn rather than blurred so it looks the same in every renderer.
                    Circle().fill(Theme.brand.opacity(0.10)).frame(width: diameter + 36, height: diameter + 36)
                    Circle().fill(Theme.brand.opacity(0.14)).frame(width: diameter + 18, height: diameter + 18)
                    Circle().fill(hovering && action.enabled ? Theme.brandFillHover : Theme.brandFill)
                        .frame(width: diameter, height: diameter)
                } else {
                    Circle().fill(Theme.surface).frame(width: diameter, height: diameter)
                    Circle().strokeBorder(ringColor, lineWidth: 6).frame(width: diameter, height: diameter)
                }
                if status.inProgress {
                    Circle().trim(from: 0, to: 0.28)
                        .stroke(Theme.brand, style: StrokeStyle(lineWidth: 6, lineCap: .round))
                        .frame(width: diameter - 6, height: diameter - 6)
                        .rotationEffect(.degrees(turning ? 360 : 0))
                        .animation(reduceMotion ? nil : .linear(duration: 1.1).repeatForever(autoreverses: false), value: turning)
                        .onAppear { turning = true }
                        .onDisappear { turning = false }
                }
                ShieldGlyph(color: glyphColor).frame(width: diameter * 0.42, height: diameter * 0.42)
            }
            .frame(width: diameter + 40, height: diameter + 40)
            .shadow(color: Theme.cardShadow, radius: status == .connected ? 0 : 12, x: 0, y: 4)
            .contentShape(Circle().inset(by: 20))
        }
        .buttonStyle(.plain)
        .disabled(!action.enabled)
        .onHover { hovering = $0 }
        .animation(reduceMotion ? nil : .easeInOut(duration: 0.22), value: status)
        .accessibilityLabel(accessibilityName)
        .accessibilityValue(status.title)
        .keyboardShortcut(.return, modifiers: [.command])
    }

    private var ringColor: Color {
        switch status {
        case .unavailable: Theme.dangerText
        case .preparing, .connecting, .stopping: Theme.stroke
        default: hovering && action.enabled ? Theme.brand : Theme.strokeStrong
        }
    }

    private var glyphColor: Color {
        switch status {
        case .connected: Theme.onBrand
        case .unavailable: Theme.dangerText
        case .disconnected: action.enabled ? Theme.brand : Theme.textTertiary
        default: Theme.brand
        }
    }

    private var accessibilityName: String {
        switch action {
        case .connect: "连接"
        case .disconnect: "断开"
        case .progress: status.title
        }
    }
}

// MARK: - Rate chart

/// The last minute of throughput: download filled, upload as a line, labelled in the legend.
struct RateChart: View {
    let samples: [CoreRateSample]

    var body: some View {
        Chart {
            ForEach(Array(samples.enumerated()), id: \.offset) { index, sample in
                AreaMark(x: .value("秒", index), y: .value("速率", sample.download), series: .value("方向", "下载"))
                    .foregroundStyle(Theme.brand.opacity(0.18))
                LineMark(x: .value("秒", index), y: .value("速率", sample.download), series: .value("方向", "下载"))
                    .foregroundStyle(Theme.brand)
                LineMark(x: .value("秒", index), y: .value("速率", sample.upload), series: .value("方向", "上传"))
                    .foregroundStyle(Theme.warnText.opacity(0.8))
                    .lineStyle(StrokeStyle(lineWidth: 1.5, dash: [3, 3]))
            }
        }
        .chartXAxis(.hidden)
        .chartYAxis {
            AxisMarks(values: .automatic(desiredCount: 3)) { _ in
                AxisGridLine().foregroundStyle(Theme.chartGrid)
            }
        }
        .chartLegend(.hidden)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("最近一分钟的速率曲线")
    }
}

/// A small legend swatch with its word, for the chart.
struct LegendItem: View {
    let color: Color
    let text: String
    var dashed = false

    var body: some View {
        HStack(spacing: 5) {
            Capsule().fill(color).frame(width: 12, height: 3).opacity(dashed ? 0.8 : 1)
            Text(text).font(.system(size: 11)).foregroundStyle(Theme.textSecondary)
        }
    }
}

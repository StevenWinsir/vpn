import AsterLinkShared
import SwiftUI

/// The home page: one big button and its status, then the line, the plan and the live rate.
struct ConnectPage: View {
    @Bindable var session: SessionController
    @Bindable var shell: AppShell

    private var snap: CoreSnapshot { session.snapshot }
    private var status: ConnectionStatus { ConnectionStatus(snap.state) }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                PageHeader(title: "连接")
                notices
                hero
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 200), spacing: 16, alignment: .top)], alignment: .leading, spacing: 16) {
                    lineCard
                    planCard
                    rateCard
                }
            }
            .padding(Theme.pagePadding)
            .frame(maxWidth: 980)
            .frame(maxWidth: .infinity)
        }
    }

    // MARK: notices

    /// Why Connect is unavailable, or what went wrong last, each with at most one next step.
    @ViewBuilder private var notices: some View {
        VStack(spacing: 8) {
            if let message = session.message {
                NoticeBar(kind: .warning, text: message)
            }
            if let blocker = Blocker.of(session) {
                switch blocker.step {
                case .purchase:
                    NoticeBar(kind: .danger, text: blocker.text, actionTitle: "购买套餐") { NSWorkspace.shared.open(session.plansURL) }
                case .chooseLine:
                    NoticeBar(kind: .warning, text: blocker.text, actionTitle: "选择线路") { shell.page = .lines }
                case .refresh:
                    NoticeBar(kind: .warning, text: blocker.text, actionTitle: "刷新") { Task { await session.refresh() } }
                case nil:
                    NoticeBar(kind: blocker.severe ? .danger : .warning, text: blocker.text)
                }
            } else if snap.state != .connected, let failure = snap.lastError, session.message == nil {
                NoticeBar(kind: .danger, text: failure.message)
            }
        }
    }

    // MARK: hero

    private var hero: some View {
        let action = PrimaryAction(session)
        return VStack(spacing: 16) {
            ConnectButton(status: status, action: action) { perform(action) }
            VStack(spacing: 6) {
                Text(status.title)
                    .font(.system(size: 26, weight: .semibold))
                    .foregroundStyle(status.tone == .idle ? Theme.textPrimary : Theme.color(for: status.tone))
                Text(detail).font(.system(size: 13)).foregroundStyle(Theme.textSecondary).multilineTextAlignment(.center)
            }
            .accessibilityElement(children: .combine)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 8)
    }

    private var detail: String {
        if status == .connected, let node = snap.selectedNode { return "已通过“\(node.name)”为你建立安全连接" }
        return status.detail
    }

    private func perform(_ action: PrimaryAction) {
        switch action {
        case .connect(true): Task { await session.connect() }
        case .disconnect(true): Task { await session.disconnect() }
        default: break
        }
    }

    // MARK: cards

    private var lineCard: some View {
        InfoCard(title: "当前线路", systemImage: "globe.asia.australia") {
            if let node = snap.selectedNode {
                Text(node.name).font(.system(size: 16, weight: .semibold)).foregroundStyle(Theme.textPrimary).lineLimit(1)
                HStack(spacing: 6) {
                    if !node.region.isEmpty {
                        Text(node.region).font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
                    }
                    LineTag(node: node)
                }
                Text("线路倍率 \(node.rateLabel)").font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
            } else {
                Text("未选择线路").font(.system(size: 16, weight: .semibold)).foregroundStyle(Theme.textSecondary)
                Text("选择一条线路后即可连接").font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
            }
        } footer: {
            Button(snap.selectedNode == nil ? "选择线路" : "更换") { shell.page = .lines }
                .buttonStyle(SecondaryButtonStyle(height: 28))
        }
    }

    private var planCard: some View {
        InfoCard(title: "套餐", systemImage: "crown") {
            if let entitlement = snap.entitlement, entitlement.accountAuthorized {
                HStack(alignment: .firstTextBaseline) {
                    Text(entitlement.planName.isEmpty ? "当前套餐" : entitlement.planName)
                        .font(.system(size: 16, weight: .semibold)).foregroundStyle(Theme.textPrimary).lineLimit(1)
                    Spacer(minLength: 6)
                    if let days = Plan.daysLeft(entitlement.subscriptionExpiresAt) {
                        Text(days == 0 ? "今天到期" : "剩余 \(days) 天").font(.system(size: 12))
                            .foregroundStyle(days <= 3 ? Theme.warnText : Theme.textSecondary)
                    }
                }
                if entitlement.totalBytes > 0 {
                    UsageBar(remaining: 1 - Plan.usedFraction(entitlement), exhausted: entitlement.remainingBytes == 0)
                    Text("剩余 \(Formatting.bytes(entitlement.remainingBytes)) / \(Formatting.bytes(entitlement.totalBytes))")
                        .font(.system(size: 12).monospacedDigit()).foregroundStyle(Theme.textPrimary)
                }
                if let metering = snap.metering, metering.estimatedRemainingBytes != entitlement.remainingBytes,
                   snap.state == .connected || metering.uploadBytes + metering.downloadBytes > 0 {
                    Text("预估剩余 \(Formatting.bytes(metering.estimatedRemainingBytes))" + (metering.pending ? " · 正在同步用量" : ""))
                        .font(.system(size: 11).monospacedDigit()).foregroundStyle(Theme.textSecondary)
                }
                Text("\(Formatting.date(entitlement.subscriptionExpiresAt)) 到期").font(.system(size: 11)).foregroundStyle(Theme.textTertiary)
            } else {
                Text("暂无有效套餐").font(.system(size: 16, weight: .semibold)).foregroundStyle(Theme.dangerText)
                Text(snap.entitlement?.reasonMessage.isEmpty == false ? snap.entitlement!.reasonMessage : "购买或续费后即可连接")
                    .font(.system(size: 12)).foregroundStyle(Theme.textSecondary).fixedSize(horizontal: false, vertical: true)
            }
        } footer: {
            HStack(spacing: 8) {
                Link(snap.entitlement?.accountAuthorized == true ? "续费" : "购买套餐", destination: session.plansURL)
                    .buttonStyle(SecondaryButtonStyle(height: 28))
                if snap.entitlement?.accountAuthorized != true {
                    Button("刷新") { Task { await session.refresh() } }.buttonStyle(SecondaryButtonStyle(height: 28))
                }
            }
        }
    }

    private var rateCard: some View {
        InfoCard(title: "实时速率", systemImage: "waveform.path.ecg") {
            if let metering = snap.metering, snap.state == .connected || metering.uploadBytes + metering.downloadBytes > 0 {
                HStack(spacing: 16) {
                    rate("arrow.down", "下载", metering.downloadBytesPerSecond)
                    rate("arrow.up", "上传", metering.uploadBytesPerSecond)
                }
                if !metering.history.isEmpty {
                    RateChart(samples: Array(metering.history.suffix(60))).frame(height: 54)
                    HStack(spacing: 12) {
                        LegendItem(color: Theme.brand, text: "下载")
                        LegendItem(color: Theme.warnText, text: "上传", dashed: true)
                        Spacer()
                        Text("最近 60 秒").font(.system(size: 11)).foregroundStyle(Theme.textTertiary)
                    }
                }
                let physical = metering.uploadBytes + metering.downloadBytes
                Text("本次已用 \(Formatting.bytes(physical)) · 按倍率计 \(Formatting.bytes(Formatting.billedBytes(physical: physical, ratePermille: metering.ratePermille)))")
                    .font(.system(size: 11).monospacedDigit()).foregroundStyle(Theme.textSecondary)
            } else {
                Text("—").font(.system(size: 16, weight: .semibold)).foregroundStyle(Theme.textTertiary)
                Text("连接后显示上传、下载速率").font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
            }
        } footer: {
            EmptyView()
        }
    }

    private func rate(_ symbol: String, _ word: String, _ value: Int64) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Label(word, systemImage: symbol).font(.system(size: 11)).foregroundStyle(Theme.textSecondary)
            Text(Formatting.speed(value)).font(.system(size: 15, weight: .semibold).monospacedDigit()).foregroundStyle(Theme.textPrimary)
                .lineLimit(1).minimumScaleFactor(0.7)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(word) \(Formatting.speed(value))")
    }
}

/// The remaining share of the plan's traffic, as a thin bar in the brand colour.
struct UsageBar: View {
    let remaining: Double
    let exhausted: Bool

    var body: some View {
        GeometryReader { proxy in
            ZStack(alignment: .leading) {
                Capsule().fill(Theme.surfaceMuted)
                Capsule().fill(exhausted ? Theme.dangerText : Theme.brand)
                    .frame(width: max(0, min(1, remaining)) * proxy.size.width)
            }
        }
        .frame(height: 6)
        .accessibilityElement()
        .accessibilityLabel("剩余流量")
        .accessibilityValue("\(Int((max(0, min(1, remaining)) * 100).rounded()))%")
    }
}

/// One of the three equal cards under the connect button.
struct InfoCard<Content: View, Footer: View>: View {
    let title: String
    let systemImage: String
    @ViewBuilder var content: Content
    @ViewBuilder var footer: Footer

    var body: some View {
        Card {
            VStack(alignment: .leading, spacing: 8) {
                Label(title, systemImage: systemImage)
                    .font(.system(size: 12, weight: .medium)).foregroundStyle(Theme.textSecondary)
                content
                Spacer(minLength: 0)
                footer
            }
            .frame(maxWidth: .infinity, minHeight: 150, alignment: .topLeading)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(title)
    }
}

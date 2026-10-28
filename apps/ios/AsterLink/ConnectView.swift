import AsterLinkKit
import Charts
import SwiftUI

/// The connect tab: one big button, the status words, the chosen line, live traffic and the plan.
struct ConnectView: View {
    @Environment(AppModel.self) private var model
    let session: SessionController

    private var state: SessionState { session.state }
    private var view: ConnectionView { state.connection }
    private var snapshot: CoreSnapshot { state.snapshot }

    var body: some View {
        ScrollView {
            VStack(spacing: 20) {
                notices
                hero
                lineCard
                if view.status == .connected || view.status == .reconfirming || view.status == .waitingNetwork { trafficCard }
                planCard
            }
            .padding(.horizontal, Theme.pageMargin)
            .padding(.vertical, 16)
            .frame(maxWidth: 560)
            .frame(maxWidth: .infinity)
        }
        .background(Theme.background.ignoresSafeArea())
        .navigationTitle("AsterLink")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .topBarTrailing) { planBadge } }
        .refreshable { await session.refresh() }
        .sheet(isPresented: Binding(get: { state.needsVPNSetup }, set: { if !$0 { session.cancelVPNSetup() } })) {
            VPNSetupSheet(proceed: { Task { await session.continueVPNSetup() } }, later: { session.cancelVPNSetup() })
                .presentationDetents([.medium, .large])
        }
    }

    // MARK: pieces

    @ViewBuilder private var planBadge: some View {
        if let plan = snapshot.entitlement?.planName, !plan.isEmpty {
            Button { model.tab = .account } label: {
                Label(plan, systemImage: "crown.fill").labelStyle(.titleAndIcon).font(.footnote.weight(.semibold))
            }
            .accessibilityIdentifier("plan-badge")
        }
    }

    @ViewBuilder private var notices: some View {
        VStack(spacing: 8) {
            if let message = state.message {
                Banner(kind: .error, text: ErrorText.text(for: message), identifier: "message-banner") { session.dismissMessage() }
            }
            if let warning = state.warning {
                Banner(kind: .warning, text: ErrorText.text(for: warning), identifier: "warning-banner") { session.dismissMessage() }
            }
            if let problem = state.coreProblem {
                Banner(kind: .error, text: ErrorText.text(for: problem), identifier: "core-banner")
            }
            if let failure = view.failure, failure != state.message, view.status != .connected {
                Banner(kind: view.needsUpdate ? .warning : .error, text: ErrorText.text(for: failure), identifier: "failure-banner")
            }
        }
    }

    private var hero: some View {
        VStack(spacing: 14) {
            ConnectButton(view: view, action: primaryAction)
            statusText
            if let detail = ErrorText.statusDetail(view.status) {
                Text(detail).font(.footnote).foregroundStyle(Theme.textSecondary).multilineTextAlignment(.center)
                    .accessibilityIdentifier("status-detail")
            }
            if view.cancellable {
                Button("取消") { Task { await session.cancelConnect() } }
                    .buttonStyle(SecondaryButtonStyle()).frame(maxWidth: 200)
                    .accessibilityIdentifier("cancel-connect")
            }
            if view.showConnect, let blocker = view.blocker {
                Text(ErrorText.text(for: blocker))
                    .font(.subheadline).foregroundStyle(view.needsPurchase ? Theme.error : Theme.textSecondary)
                    .multilineTextAlignment(.center).padding(.horizontal, 8)
                    .accessibilityIdentifier("blocker-text")
            }
        }
        .padding(.top, 8)
    }

    private var statusText: some View {
        TimelineView(.periodic(from: .now, by: 1)) { _ in
            Text(statusLine)
                .font(.title.weight(.semibold).monospacedDigit())
                .foregroundStyle(view.tone == .idle ? Theme.textPrimary : Theme.color(for: view.tone))
                .accessibilityIdentifier("status-text")
                .accessibilityAddTraits(.updatesFrequently)
        }
    }

    private var statusLine: String {
        let words = ErrorText.status(view.status)
        guard view.status == .connected, let since = state.connectedSinceMillis else { return words }
        return "\(words) · \(Formatting.elapsed(milliseconds: SessionController.monotonicMillis() - since))"
    }

    private func primaryAction() {
        if view.showDisconnect { Task { await session.disconnect() } }
        else if view.showConnect && view.canConnect { Task { await session.connect() } }
    }

    private var lineCard: some View {
        Button { model.tab = .lines } label: {
            Card {
                HStack(spacing: 12) {
                    Image(systemName: "globe.asia.australia").font(.title3).foregroundStyle(Theme.primary).accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 4) {
                        Text("当前线路").font(.footnote).foregroundStyle(Theme.textSecondary)
                        if let node = snapshot.selectedNode {
                            Text(node.name).font(.headline).foregroundStyle(Theme.textPrimary).accessibilityIdentifier("line-name")
                            Text(lineSummary(node)).font(.subheadline).foregroundStyle(Theme.textSecondary).accessibilityIdentifier("line-summary")
                        } else {
                            Text("未选择线路").font(.headline).foregroundStyle(Theme.textSecondary).accessibilityIdentifier("line-name")
                        }
                    }
                    Spacer(minLength: 0)
                    Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(Theme.textSecondary)
                }
            }
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .combine)
        .accessibilityHint("打开线路页")
        .accessibilityIdentifier("line-card")
    }

    private var trafficCard: some View {
        let metering = snapshot.metering
        return Card {
            VStack(alignment: .leading, spacing: 12) {
                // At large text sizes the two rates stack instead of wrapping inside each other.
                ViewThatFits(in: .horizontal) {
                    HStack {
                        rate("arrow.up", "上传", metering?.uploadBytesPerSecond ?? 0, id: "rate-up")
                        Spacer()
                        rate("arrow.down", "下载", metering?.downloadBytesPerSecond ?? 0, id: "rate-down")
                    }
                    VStack(alignment: .leading, spacing: 8) {
                        rate("arrow.up", "上传", metering?.uploadBytesPerSecond ?? 0, id: "rate-up")
                        rate("arrow.down", "下载", metering?.downloadBytesPerSecond ?? 0, id: "rate-down")
                    }
                }
                .font(.headline.monospacedDigit()).foregroundStyle(Theme.textPrimary)
                RateChart(samples: Array((metering?.history ?? []).suffix(60)))
                    .frame(height: 72)
                if let usage = state.usage {
                    Text("本次已用 \(Formatting.bytes(usage.physicalBytes)) · 按倍率计 \(Formatting.bytes(usage.billedBytes))")
                        .font(.footnote.monospacedDigit()).foregroundStyle(Theme.textSecondary)
                        .accessibilityIdentifier("usage-text")
                }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("traffic-card")
    }

    private func rate(_ symbol: String, _ word: String, _ value: Int64, id: String) -> some View {
        HStack(spacing: 6) {
            Image(systemName: symbol)
            Text(Formatting.speed(value)).lineLimit(1).minimumScaleFactor(0.7)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(word) \(Formatting.speed(value))")
        .accessibilityIdentifier(id)
    }

    @ViewBuilder private var planCard: some View {
        if let entitlement = snapshot.entitlement {
            let plan = entitlement.planName.isEmpty ? "无有效套餐" : "\(entitlement.planName) 套餐"
            // The synced balance is the server's; the estimate subtracts traffic not yet synced.
            let estimated = snapshot.metering?.estimatedRemainingBytes ?? entitlement.remainingBytes
            Card {
                VStack(alignment: .leading, spacing: 10) {
                    HStack {
                        Text(plan).font(.headline).foregroundStyle(Theme.textPrimary)
                        Spacer()
                        if let expires = entitlement.subscriptionExpires {
                            Text("\(Formatting.date(expires)) 到期").font(.footnote).foregroundStyle(Theme.textSecondary)
                        }
                    }
                    if entitlement.totalBytes > 0 {
                        ProgressView(value: 1 - Formatting.usedFraction(totalBytes: entitlement.totalBytes, remainingBytes: entitlement.remainingBytes))
                            .tint(entitlement.remainingBytes == 0 ? Theme.error : Theme.primary)
                            .accessibilityHidden(true)
                        Text("剩余 \(Formatting.bytes(entitlement.remainingBytes)) / \(Formatting.bytes(entitlement.totalBytes))")
                            .font(.subheadline.monospacedDigit()).foregroundStyle(Theme.textPrimary)
                            .accessibilityIdentifier("plan-remaining")
                        if state.usage != nil, estimated != entitlement.remainingBytes {
                            Text("预估剩余 \(Formatting.bytes(estimated))" + (snapshot.metering?.pending == true ? " · 正在同步用量" : ""))
                                .font(.footnote.monospacedDigit()).foregroundStyle(Theme.textSecondary)
                                .accessibilityIdentifier("plan-estimated")
                        }
                    } else if !entitlement.accountAuthorized {
                        Text("请到官网管理套餐").font(.subheadline).foregroundStyle(Theme.textSecondary)
                    }
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("plan-card")
        }
    }

    private func lineSummary(_ node: NodeView) -> String {
        "\(ErrorText.lineLabel(dedicated: node.isDedicated)) · 线路倍率 \(Formatting.rate(node.ratePermille))"
    }
}

/// The 168 pt power button. The ring shows the state; the words under it say the same thing.
struct ConnectButton: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    let view: ConnectionView
    let action: () -> Void
    @State private var spin = false

    private var ring: Color { Theme.color(for: view.tone) }
    private var enabled: Bool { view.showDisconnect || (view.showConnect && view.canConnect) }

    var body: some View {
        Button(action: action) {
            ZStack {
                Circle().fill(view.tone == .good ? Theme.brandFill : Theme.surface)
                Circle().stroke(view.tone == .idle ? Theme.stroke : ring.opacity(0.35), lineWidth: 8)
                if view.showProgress {
                    Circle().trim(from: 0, to: 0.28)
                        .stroke(ring, style: StrokeStyle(lineWidth: 8, lineCap: .round))
                        .rotationEffect(.degrees(spin && !reduceMotion ? 360 : 0))
                        .animation(reduceMotion ? nil : .linear(duration: 1.1).repeatForever(autoreverses: false), value: spin)
                }
                Image(systemName: "power")
                    .font(.system(size: 56, weight: .semibold))
                    .foregroundStyle(view.tone == .good ? .white : (enabled ? Theme.primary : Theme.textSecondary))
            }
            .frame(width: 168, height: 168)
            .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .onAppear { spin = true }
        .sensoryFeedback(.success, trigger: view.status == .connected)
        .accessibilityLabel(label)
        .accessibilityValue(ErrorText.status(view.status))
        .accessibilityIdentifier("connect-button")
    }

    private var label: String {
        if view.showDisconnect { return "断开连接" }
        if view.showConnect { return view.canConnect ? "连接" : "无法连接" }
        return "请稍候"
    }
}

/// 60 seconds of rates, one line each way (Swift Charts; bounded, no third party).
struct RateChart: View {
    let samples: [MeterSample]

    var body: some View {
        Chart {
            ForEach(Array(samples.enumerated()), id: \.offset) { index, sample in
                LineMark(x: .value("秒", index), y: .value("速率", sample.download), series: .value("方向", "下行"))
                    .foregroundStyle(Theme.primary).interpolationMethod(.monotone)
                LineMark(x: .value("秒", index), y: .value("速率", sample.upload), series: .value("方向", "上行"))
                    .foregroundStyle(Theme.pending).interpolationMethod(.monotone)
            }
        }
        .chartXAxis(.hidden).chartYAxis(.hidden)
        .chartLegend(.hidden)
        .accessibilityLabel("最近 60 秒速率曲线")
        .accessibilityIdentifier("rate-chart")
    }
}

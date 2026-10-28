import AsterLinkShared
import SwiftUI

/// Lines grouped by region, with search, a type filter and refresh. Choosing a line while connected
/// asks first, because the switch ends the current connection.
struct LinesPage: View {
    @Bindable var session: SessionController
    @Bindable var shell: AppShell
    @State private var pendingSwitch: CoreNode?

    private var snap: CoreSnapshot { session.snapshot }

    var body: some View {
        let groups = NodeList.groups(snap.nodes, query: shell.lineQuery, filter: shell.lineFilter)
        VStack(alignment: .leading, spacing: 16) {
            PageHeader(title: "线路", subtitle: snap.nodes.isEmpty ? nil : "共 \(snap.nodes.count) 条线路")
            controls
            if let reason = emptyReason(filtered: groups.isEmpty) {
                emptyState(reason)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 18, pinnedViews: []) {
                        ForEach(groups) { group in
                            VStack(alignment: .leading, spacing: 8) {
                                Text("\(group.region) · \(group.nodes.count)")
                                    .font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.textSecondary)
                                    .accessibilityAddTraits(.isHeader)
                                Card(padding: 4) {
                                    VStack(spacing: 0) {
                                        ForEach(Array(group.nodes.enumerated()), id: \.element.id) { index, node in
                                            if index > 0 { Divider().overlay(Theme.stroke).padding(.horizontal, 12) }
                                            LineRow(node: node, selected: node.id == snap.selectedNodeID) { choose(node) }
                                                .disabled(session.busy)
                                        }
                                    }
                                }
                            }
                        }
                    }
                    .padding(.bottom, 8)
                }
            }
        }
        .padding(Theme.pagePadding)
        .frame(maxWidth: 980, maxHeight: .infinity, alignment: .top)
        .frame(maxWidth: .infinity)
        .alert("更换线路？", isPresented: Binding(get: { pendingSwitch != nil }, set: { if !$0 { pendingSwitch = nil } }),
               presenting: pendingSwitch) { node in
            Button("断开并更换") { Task { await session.select(node.id) } }
            Button("取消", role: .cancel) {}
        } message: { node in
            Text("更换到“\(node.name)”会断开当前连接，更换后需要重新连接。")
        }
    }

    private var controls: some View {
        HStack(spacing: 12) {
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass").foregroundStyle(Theme.textTertiary).accessibilityHidden(true)
                TextField("搜索线路或地区", text: $shell.lineQuery).textFieldStyle(.plain).font(.system(size: 13))
                if !shell.lineQuery.isEmpty {
                    Button { shell.lineQuery = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(Theme.textTertiary) }
                        .buttonStyle(.plain).accessibilityLabel("清除搜索")
                }
            }
            .padding(.horizontal, 10)
            .frame(height: 30)
            .frame(maxWidth: 280)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.controlRadius, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.controlRadius, style: .continuous).strokeBorder(Theme.strokeStrong, lineWidth: 1))

            Picker("线路类型", selection: $shell.lineFilter) {
                ForEach(LineFilter.allCases) { Text($0.title).tag($0) }
            }
            .pickerStyle(.segmented).labelsHidden().fixedSize()

            Spacer(minLength: 0)
            Button { Task { await session.refresh() } } label: { Label("刷新", systemImage: "arrow.clockwise") }
                .buttonStyle(SecondaryButtonStyle(height: 30))
                .disabled(session.busy)
        }
    }

    /// So "no plan", "no lines" and "nothing matches" are never confused.
    private func emptyReason(filtered: Bool) -> String? {
        if snap.entitlement?.accountAuthorized != true { return "购买套餐后即可选择线路" }
        if snap.nodes.isEmpty { return snap.catalogRevision.isEmpty ? "正在同步线路…" : "暂无可用线路，请稍后刷新" }
        return filtered ? "没有符合条件的线路" : nil
    }

    private func emptyState(_ text: String) -> some View {
        VStack(spacing: 12) {
            Image(systemName: "globe.asia.australia").font(.system(size: 34)).foregroundStyle(Theme.textTertiary)
                .accessibilityHidden(true)
            Text(text).font(.system(size: 14)).foregroundStyle(Theme.textSecondary)
            if snap.entitlement?.accountAuthorized != true {
                Link("购买套餐", destination: session.plansURL).buttonStyle(PrimaryButtonStyle())
            } else if !shell.lineQuery.isEmpty || shell.lineFilter != .all, !snap.nodes.isEmpty {
                Button("清除筛选") { shell.lineQuery = ""; shell.lineFilter = .all }.buttonStyle(SecondaryButtonStyle())
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(.vertical, 40)
    }

    private func choose(_ node: CoreNode) {
        guard node.id != snap.selectedNodeID else { return }
        if snap.state == .connected {
            pendingSwitch = node
        } else {
            Task { await session.select(node.id) }
        }
    }
}

struct LineRow: View {
    let node: CoreNode
    let selected: Bool
    let action: () -> Void
    @State private var hovering = false
    @Environment(\.isEnabled) private var isEnabled

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                    .font(.system(size: 16))
                    .foregroundStyle(selected ? Theme.brand : Theme.strokeStrong)
                    .accessibilityHidden(true)
                // The region is the group heading above, so the row shows only the name.
                Text(node.name).font(.system(size: 14, weight: selected ? .semibold : .regular)).foregroundStyle(Theme.textPrimary)
                    .lineLimit(1)
                Spacer(minLength: 8)
                LineTag(node: node)
                Text("线路倍率 \(node.rateLabel)").font(.system(size: 12).monospacedDigit()).foregroundStyle(Theme.textSecondary)
                    .frame(minWidth: 84, alignment: .trailing)
                Text("已选").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.brand)
                    .opacity(selected ? 1 : 0)
                    .frame(width: 30, alignment: .trailing)
            }
            .padding(.horizontal, 12).padding(.vertical, 11)
            .contentShape(Rectangle())
            .background(background, in: RoundedRectangle(cornerRadius: Theme.controlRadius, style: .continuous))
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .animation(.easeOut(duration: 0.15), value: hovering)
        // The button stays the accessibility element (so it can be pressed); it reads as one sentence.
        .accessibilityLabel("\(node.name)，\(node.region)，\(node.lineLabel)，线路倍率 \(node.rateLabel)")
        .accessibilityValue(selected ? "已选" : "")
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private var background: Color {
        if selected { return Theme.brandSoft }
        return hovering && isEnabled ? Theme.surfaceMuted : .clear
    }
}

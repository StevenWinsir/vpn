import AsterLinkKit
import SwiftUI

/// The lines tab: the catalog the administrator configured for the user's plan, grouped by
/// region, searchable. Names, regions, line type and the billing multiple are all the user ever
/// sees of a line; addresses, ports and credentials never leave the Core.
struct LinesView: View {
    @Environment(AppModel.self) private var model
    let session: SessionController

    @State private var query = ""
    @State private var filter: LineFilter = .all
    @State private var pendingSwitch: NodeRow?
    @State private var seeded = false

    private var state: SessionState { session.state }
    private var snapshot: CoreSnapshot { state.snapshot }

    private var groups: [RegionGroup] {
        NodeList.build(snapshot, query: query, filter: filter) { ErrorText.lineLabel(dedicated: $0) }
    }

    private var emptyReason: String? {
        NodeList.emptyReason(snapshot, filtered: !query.trimmingCharacters(in: .whitespaces).isEmpty || filter != .all)
    }

    var body: some View {
        List {
            if let message = state.message {
                Banner(kind: .error, text: ErrorText.text(for: message), identifier: "message-banner") { session.dismissMessage() }
                    .listRowBackground(Color.clear).listRowSeparator(.hidden)
            }
            if let reason = emptyReason, groups.isEmpty {
                empty(reason).listRowBackground(Color.clear).listRowSeparator(.hidden)
            } else {
                Section { Picker("线路类型", selection: $filter) {
                    Text("全部").tag(LineFilter.all)
                    Text("专线").tag(LineFilter.dedicated)
                    Text("普通").tag(LineFilter.direct)
                }
                .pickerStyle(.segmented)
                .accessibilityIdentifier("lines-filter") }
                .listRowBackground(Color.clear)
                ForEach(groups) { group in
                    Section {
                        ForEach(group.rows) { row in rowView(row) }
                    } header: {
                        HStack {
                            SectionTitle(text: group.region.isEmpty ? "其他地区" : group.region)
                            Spacer()
                            Text("\(group.rows.count) 条").font(.footnote).foregroundStyle(Theme.textSecondary)
                        }
                    }
                }
            }
        }
        .scrollContentBackground(.hidden)
        .background(Theme.background.ignoresSafeArea())
        .navigationTitle("线路")
        .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "搜索线路或地区")
        .refreshable { await session.refresh() }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { Task { await session.refresh() } } label: { Image(systemName: "arrow.clockwise") }
                    .disabled(state.busy).accessibilityLabel("刷新线路").accessibilityIdentifier("lines-refresh")
            }
        }
        .accessibilityIdentifier("lines-list")
        .onAppear { if !seeded { seeded = true; query = model.launchScreen.linesQuery } }
        .confirmationDialog("切换线路", isPresented: Binding(get: { pendingSwitch != nil }, set: { if !$0 { pendingSwitch = nil } }), titleVisibility: .visible, presenting: pendingSwitch) { row in
            Button("断开并切换到 \(row.name)", role: .destructive) { Task { await session.switchTo(row.id) } }
            Button("取消", role: .cancel) {}
        } message: { _ in
            Text("将断开当前连接并结算本次流量，切换后需要重新连接。")
        }
    }

    private func rowView(_ row: NodeRow) -> some View {
        Button { choose(row) } label: {
            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(row.name).font(.body.weight(.medium)).foregroundStyle(Theme.textPrimary)
                    Text("\(ErrorText.lineLabel(dedicated: row.dedicated)) · 计费 \(Formatting.rate(row.ratePermille))")
                        .font(.subheadline).foregroundStyle(row.dedicated ? Theme.primary : Theme.textSecondary)
                    if row.nodeMetered {
                        Text("节点计量").font(.footnote).foregroundStyle(Theme.textSecondary)
                    }
                }
                Spacer(minLength: 0)
                if row.selected {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(Theme.primary).accessibilityHidden(true)
                }
            }
            .frame(minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .listRowBackground(Theme.surface)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(row.name)，\(ErrorText.lineLabel(dedicated: row.dedicated))，计费 \(Formatting.rate(row.ratePermille))" + (row.nodeMetered ? "，节点计量" : ""))
        .accessibilityValue(row.selected ? "已选择" : "")
        .accessibilityAddTraits(row.selected ? [.isButton, .isSelected] : .isButton)
        .accessibilityIdentifier("line-row-\(row.name)")
    }

    private func choose(_ row: NodeRow) {
        if state.snapshot.tunnelBusy && row.id != snapshot.selectedNodeID {
            // Switching while a tunnel exists ends it (and settles it) first.
            pendingSwitch = row
        } else {
            Task { await session.select(row.id); if state.message == nil { model.tab = .connect } }
        }
    }

    @ViewBuilder private func empty(_ reason: String) -> some View {
        VStack(spacing: 10) {
            if reason == LocalCodes.syncing { ProgressView().accessibilityHidden(true) } else {
                Image(systemName: reason == LocalCodes.noMatch ? "magnifyingglass" : reason == LocalCodes.noPlan ? "creditcard" : "tray")
                    .font(.system(size: 40)).foregroundStyle(Theme.textSecondary).accessibilityHidden(true)
            }
            Text(reason == LocalCodes.noPlan ? "购买套餐后可选择线路" : ErrorText.local(reason))
                .font(.headline).foregroundStyle(Theme.textPrimary).multilineTextAlignment(.center)
            if reason == LocalCodes.noPlan {
                Text("请到官网管理套餐").font(.subheadline).foregroundStyle(Theme.textSecondary)
            }
        }
        .frame(maxWidth: .infinity).padding(.vertical, 48)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("lines-empty-\(reason)")
    }
}

using AsterLink.Shared.Service;
using AsterLink.Shared.Snapshot;

namespace AsterLink.Shared.Client;

public enum Tone { Idle, Busy, Good, Bad }

/// <summary>What the connect page shows for one snapshot. Derived only from the Core's verdict; nothing here decides authorization.</summary>
public sealed record ConnectionView(
    string StatusText,
    string Detail,
    Tone Tone,
    bool ShowConnect,
    bool ShowDisconnect,
    bool ShowProgress,
    bool CanConnect,
    string DisabledReason)
{
    public static ConnectionView From(CoreSnapshot snapshot, bool operationBusy)
    {
        var authorized = snapshot.Entitlement?.AccountAuthorized == true;
        var selected = snapshot.SelectedNodeId.Length > 0 && snapshot.Nodes.Any(n => n.Id == snapshot.SelectedNodeId);
        var reason = !authorized
            ? (snapshot.Entitlement?.ReasonMessage is { Length: > 0 } text ? text : NoPlanText)
            : snapshot.Nodes.Count == 0 ? NodeList.NoLinesText(snapshot)
            : !selected ? "请先选择一条线路"
            : "";

        switch (snapshot.State)
        {
            case "connected":
                return new("已连接", "已为你建立安全连接", Tone.Good, false, true, false, false, "");
            case "connecting":
                return new("正在连接…", "正在为你建立安全连接", Tone.Busy, false, false, true, false, "");
            case "preparing":
                return new("正在准备…", "正在准备连接，请稍候", Tone.Busy, false, false, true, false, "");
            case "stopping":
                return new("正在断开…", "正在断开当前连接", Tone.Busy, false, false, true, false, "");
        }

        var (text2, tone) = snapshot.State switch
        {
            "blocked" or "error" => ("无法连接", Tone.Bad),
            _ => ("未连接", Tone.Idle),
        };
        var failure = snapshot.LastError is { Message.Length: > 0 } e ? e.Message : "";
        var canConnect = reason.Length == 0 && !operationBusy;
        return new(text2, failure.Length > 0 ? failure : reason, tone, true, false, false, canConnect, reason);
    }

    /// <summary>What the page says when the account has no plan and the Core gave no reason of its own.</summary>
    public const string NoPlanText = "暂无可用套餐，请前往官网购买或续费";
}

public enum LineFilter { All, Dedicated, Direct }

public sealed record NodeRow(string Id, string Name, string Region, string LineLabel, bool IsDedicated, string RateLabel, bool Selected)
{
    public static NodeRow From(NodeView node, CoreSnapshot snapshot) => new(
        node.Id, node.Name, node.Region,
        LineLabelOf(node.LineType), node.LineType == "dedicated", Formatting.Rate(node.RatePermille),
        node.Id == snapshot.SelectedNodeId);

    /// <summary><c>direct</c> is the ordinary routed line, NOT a Clash DIRECT bypass.</summary>
    public static string LineLabelOf(string lineType) => lineType == "dedicated" ? "专线" : "普通线路";

    /// <summary>"线路倍率 1×": the rate as the website writes it.</summary>
    public string RateText => "线路倍率 " + RateLabel;

    /// <summary>What a screen reader says for the row; the selection is a word, never colour alone.</summary>
    public string SpokenName => $"{Name}，{Region}，{LineLabel}，{RateText}" + (Selected ? "，已选" : "");
}

/// <summary>The lines of one region, in the order the server listed them.</summary>
public sealed record RegionGroup(string Region, IReadOnlyList<NodeRow> Rows)
{
    public string CountText => Rows.Count + " 条";
}

public static class NodeList
{
    /// <summary>Search over the metadata the user can already see (name, region, line label); the server's order is kept.</summary>
    public static IReadOnlyList<NodeRow> Build(CoreSnapshot snapshot, string query, LineFilter filter)
    {
        var needle = query.Trim();
        return [.. snapshot.Nodes
            .Where(n => filter switch
            {
                LineFilter.Dedicated => n.LineType == "dedicated",
                LineFilter.Direct => n.LineType != "dedicated",
                _ => true,
            })
            .Where(n => needle.Length == 0
                || n.Name.Contains(needle, StringComparison.CurrentCultureIgnoreCase)
                || n.Region.Contains(needle, StringComparison.CurrentCultureIgnoreCase)
                || NodeRow.LineLabelOf(n.LineType).Contains(needle, StringComparison.CurrentCultureIgnoreCase))
            .Select(n => NodeRow.From(n, snapshot))];
    }

    /// <summary>
    /// Rows grouped by region for display. Nothing is re-ordered: inside a region the server's order is kept, and the
    /// regions appear in the order of their first line in the server's list.
    /// </summary>
    public static IReadOnlyList<RegionGroup> Group(IReadOnlyList<NodeRow> rows)
    {
        var order = new List<string>();
        var groups = new Dictionary<string, List<NodeRow>>();
        foreach (var row in rows)
        {
            var key = row.Region.Trim();
            if (!groups.TryGetValue(key, out var list)) { groups[key] = list = []; order.Add(key); }
            list.Add(row);
        }
        return [.. order.Select(key => new RegionGroup(key.Length == 0 ? "其他" : key, groups[key]))];
    }

    /// <summary>The text of the empty state, so "no plan", "no nodes" and "nothing matches" are never confused.</summary>
    public static string EmptyText(CoreSnapshot snapshot, bool filtered) =>
        snapshot.Entitlement?.AccountAuthorized != true ? "购买套餐后即可选择线路"
        : snapshot.Nodes.Count == 0 ? NoLinesText(snapshot)
        : filtered ? "没有符合条件的线路" : "";

    /// <summary>An authorized account without lines: still syncing (no catalog yet) or really none.</summary>
    public static string NoLinesText(CoreSnapshot snapshot) =>
        snapshot.CatalogRevision.Length == 0 ? "正在同步线路…" : "暂无可用线路，请稍后刷新";
}

/// <summary>Stable service/transport error codes → text for the user. The Core's own codes carry their own Chinese message.</summary>
public static class ErrorText
{
    public static string ForService(string code) => code switch
    {
        ServiceErrors.ServiceNotInstalled => "后台服务尚未安装，请按提示完成安装后再试",
        ServiceErrors.ServiceNotRunning => "后台服务未运行，请按提示启动后再试",
        ServiceErrors.ServerRejected => "无法确认后台服务的身份，请重新安装 AsterLink",
        ServiceErrors.PeerRejected => "请从开始菜单打开已安装的 AsterLink",
        ServiceErrors.OwnerBusy => "另一位 Windows 用户正在使用 AsterLink，请等待其退出后再试",
        ServiceErrors.VersionMismatch => "应用版本不一致，请修复或重新安装 AsterLink",
        ServiceErrors.IntegrityFailed => "应用文件不完整，请修复或重新安装 AsterLink",
        ServiceErrors.RecoveryBlocked => "上次未能正常恢复网络设置，请修复 AsterLink",
        ServiceErrors.WorkerUnavailable => "暂时无法建立连接，请尝试修复 AsterLink",
        ServiceErrors.WorkerExited or ServiceErrors.WorkerUnresponsive => "连接已中断，请重新登录",
        ServiceErrors.LoginTargetMismatch => "应用安装不完整，请从官网重新下载安装",
        ServiceErrors.PayloadTooLarge or ServiceErrors.InvalidRequest or ServiceErrors.UnknownCommand => "操作失败，请稍后重试",
        _ => "后台服务暂时不可用，请稍后重试或修复 AsterLink",
    };

    /// <summary>The Core/service lost the worker: the session lived only in that process, so nothing is resumed.</summary>
    public static bool IsWorkerLost(string code) =>
        code is ServiceErrors.WorkerExited or ServiceErrors.WorkerUnresponsive or ServiceErrors.WorkerUnavailable;
}

/// <summary>
/// The text behind "复制诊断信息" (handoff_app_standardization.md D9). It is pasted to customer support, so it is
/// user-visible copy: the app version, the system version, the connection state and the last error code, with the
/// time of the report. The same six lines as macOS, iOS and Android; no token, password, line or server detail.
/// </summary>
public static class DiagnosticsReport
{
    public static string Build(string appVersion, string osVersion, string connectionState, CoreSnapshot snapshot, DateTimeOffset now)
    {
        var code = snapshot.LastError?.Code ?? "";
        var lines = new[]
        {
            "AsterLink 诊断信息",
            "时间: " + now.ToLocalTime().ToString("yyyy-MM-dd HH:mm:ss", System.Globalization.CultureInfo.InvariantCulture),
            "应用版本: " + appVersion,
            "系统版本: " + osVersion,
            "连接状态: " + connectionState.Replace("…", "", StringComparison.Ordinal),
            "错误码: " + (code.Length == 0 ? "无" : code),
        };
        return string.Join('\n', lines);
    }
}

using System.Text;
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
            ? (snapshot.Entitlement?.ReasonMessage is { Length: > 0 } text ? text : "暂无可用套餐，请前往网站购买或续费")
            : snapshot.Nodes.Count == 0 ? "管理员尚未配置可用线路"
            : !selected ? "请先在“线路”页选择一条线路"
            : "";

        switch (snapshot.State)
        {
            case "connected":
                return new("已连接", "流量正在经所选线路转发", Tone.Good, false, true, false, false, "");
            case "connecting":
                return new("正在连接…", "正在建立隧道并验证转发", Tone.Busy, false, false, true, false, "");
            case "preparing":
                return new("正在准备…", "正在校验线路与授权", Tone.Busy, false, false, true, false, "");
            case "stopping":
                return new("正在断开…", "正在关闭数据通路并结算本次流量", Tone.Busy, false, false, true, false, "");
        }

        var (text2, tone) = snapshot.State switch
        {
            "blocked" => ("不可用", Tone.Bad),
            "error" => ("出错", Tone.Bad),
            "ready" when snapshot.ConfigReady => ("已就绪，未连接", Tone.Idle),
            _ => ("未连接", Tone.Idle),
        };
        var failure = snapshot.LastError is { Message.Length: > 0 } e ? e.Message : "";
        var canConnect = reason.Length == 0 && !operationBusy;
        return new(text2, failure.Length > 0 ? failure : reason, tone, true, false, false, canConnect, reason);
    }
}

public enum LineFilter { All, Dedicated, Direct }

public sealed record NodeRow(string Id, string Name, string Region, string LineLabel, bool IsDedicated, string RateLabel, bool NodeMetered, bool Selected, bool ConfigVerified)
{
    public static NodeRow From(NodeView node, CoreSnapshot snapshot) => new(
        node.Id, node.Name, node.Region,
        LineLabelOf(node.LineType), node.LineType == "dedicated", Formatting.Rate(node.RatePermille),
        node.MeteringSource == "node_reported",
        node.Id == snapshot.SelectedNodeId,
        node.Id == snapshot.SelectedNodeId && snapshot.ConfigReady);

    /// <summary><c>direct</c> is the ordinary routed line, NOT a Clash DIRECT bypass.</summary>
    public static string LineLabelOf(string lineType) => lineType == "dedicated" ? "专线" : "普通线路";

    /// <summary>Who keeps the books for a line. A node-governed line is billed by the node itself; any other is the client-reported development ledger, which is not a commercial guarantee.</summary>
    public static string MeteringLabelOf(string meteringSource) =>
        meteringSource == "node_reported" ? "计量：节点实测，由服务器强制执行" : "计量：客户端上报（内部测试线路）";
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

    /// <summary>The text of the empty state, so "no plan", "no nodes" and "nothing matches" are never confused.</summary>
    public static string EmptyText(CoreSnapshot snapshot, bool filtered) =>
        snapshot.Entitlement?.AccountAuthorized != true ? "购买套餐后可选择线路"
        : snapshot.Nodes.Count == 0 ? "管理员尚未配置可用线路"
        : filtered ? "没有符合条件的线路" : "";
}

/// <summary>Stable service/transport error codes → text for the user. The Core's own codes carry their own Chinese message.</summary>
public static class ErrorText
{
    public static string ForService(string code, string fallback = "") => code switch
    {
        ServiceErrors.ServiceNotInstalled => "特权服务尚未安装，请在“账户与设置”中安装",
        ServiceErrors.ServiceNotRunning => "特权服务未运行，请在“账户与设置”中启动",
        ServiceErrors.ServerRejected => "无法确认本机服务的身份，已拒绝连接。请修复安装",
        ServiceErrors.PeerRejected => "服务拒绝了本程序（请从安装目录启动 AsterLink）",
        ServiceErrors.OwnerBusy => "另一位 Windows 用户正在使用 AsterLink，请等待其退出",
        ServiceErrors.VersionMismatch => "程序与服务版本不一致，请修复安装",
        ServiceErrors.IntegrityFailed => "已安装的文件与清单不一致，请修复安装",
        ServiceErrors.RecoveryBlocked => "上一次会话未能恢复网络状态，请修复安装",
        ServiceErrors.WorkerUnavailable => "代理内核无法启动，请尝试修复安装",
        ServiceErrors.WorkerExited or ServiceErrors.WorkerUnresponsive => "内核已意外退出，连接已停止，请重新登录",
        ServiceErrors.LoginTargetMismatch => "应用的服务器地址配置与安装不一致，请重新安装",
        ServiceErrors.PayloadTooLarge or ServiceErrors.InvalidRequest or ServiceErrors.UnknownCommand => "请求格式不正确",
        _ => fallback.Length > 0 ? fallback : "无法连接本机服务，请在“账户与设置”中检查特权服务状态",
    };

    /// <summary>The Core/service lost the worker: the session lived only in that process, so nothing is resumed.</summary>
    public static bool IsWorkerLost(string code) =>
        code is ServiceErrors.WorkerExited or ServiceErrors.WorkerUnresponsive or ServiceErrors.WorkerUnavailable;
}

/// <summary>Redacted diagnostics for support. Built from the Core's already-redacted snapshot; no field here can hold a token, password or node detail.</summary>
public static class DiagnosticsReport
{
    public static string Build(string appVersion, string channel, string apiHost, string osDescription, CoreSnapshot snapshot, string? serviceState)
    {
        var lines = new List<string>
        {
            $"AsterLink {appVersion} ({channel})",
            $"os {osDescription}",
            $"core {(snapshot.CoreVersion.Length == 0 ? "-" : snapshot.CoreVersion)}",
            $"service {serviceState ?? "-"}",
            $"api {apiHost}",
            $"state {(snapshot.State.Length == 0 ? "-" : snapshot.State)} generation {snapshot.Generation}",
            $"nodes {snapshot.Nodes.Count} config_ready {snapshot.ConfigReady.ToString().ToLowerInvariant()}",
        };
        if (snapshot.Entitlement is { } entitlement)
            lines.Add($"account_authorized {entitlement.AccountAuthorized.ToString().ToLowerInvariant()} reason {(entitlement.Reason.Length == 0 ? "-" : entitlement.Reason)}");
        if (snapshot.LastError is { } error) lines.Add($"last_error {error.Code}");
        var text = new StringBuilder();
        foreach (var line in lines) text.Append(line).Append('\n');
        return text.ToString();
    }
}

using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Runtime.CompilerServices;
using System.Windows;
using System.Windows.Input;
using System.Windows.Media;
using AsterLink.Platform;
using AsterLink.Shared.Client;
using AsterLink.Shared.Platform;
using AsterLink.Shared.Service;
using AsterLink.Shared.Snapshot;

namespace AsterLink.App;

public sealed record FactRow(string Label, string Value);

public sealed record CheckRow(string Name, bool Passed)
{
    public string Mark => Passed ? "通过" : "失败";
    public Brush Brush => (Brush)Application.Current.Resources[Passed ? "PassBrush" : "FailBrush"];
}

public enum AppPage { Connect, Nodes, Settings }

/// <summary>What the design preview injects in place of the real service, vault and build configuration.</summary>
public sealed record PreviewParts(SessionController Session, Shared.Env.ClientEnvironment Environment, ILoginPreferences Preferences);

/// <summary>
/// Everything the window binds to. It renders the Core's snapshot through <see cref="SessionController"/>; it
/// holds no token, no password and no node detail. When the build configuration cannot be read the login page says
/// so and nothing else works, rather than guessing a server address.
/// </summary>
public sealed class AppViewModel : INotifyPropertyChanged
{
    private const int ChartWidth = 300;
    private const int ChartHeight = 48;
    private static readonly TimeSpan ActivationRefreshGap = TimeSpan.FromSeconds(10);

    private readonly SessionController? session;
    private readonly ILoginPreferences preferences;
    private readonly Shared.Env.ClientEnvironment? environment;
    private AppPage page = AppPage.Connect;
    private LineFilter filter = LineFilter.All;
    private string search = "";
    private IReadOnlyList<NodeRow> rows = [];
    private string theme;
    private string? localMessage;
    private DateTimeOffset lastRefresh = DateTimeOffset.MinValue;

    /// <param name="preview">Design-preview mode only (<c>--preview</c>): a scripted session and in-memory preferences, no service, no vault, no network.</param>
    public AppViewModel(PreviewParts? preview = null)
    {
        var directory = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "AsterLink");
        preferences = preview?.Preferences ?? new FileLoginPreferences(directory);
        theme = preferences.Theme;

        var layout = preview is null ? InstallLayout.FromComponentDirectory(AppContext.BaseDirectory) : null;
        Service = new ServicePanel();
        Service.SessionActive = () => session?.Phase == AppPhase.SignedIn;
        Service.PropertyChanged += (_, _) => Raise(string.Empty);

        if (preview is not null)
        {
            session = preview.Session;
            environment = preview.Environment;
            session.Changed += OnSessionChanged;
        }
        else if (layout is null)
        {
            ConfigProblem = "请从安装目录或打包目录启动 AsterLink（当前位置没有受保护的构建配置）。";
        }
        else
        {
            try
            {
                environment = BuildConfig.Parse(File.ReadAllBytes(layout.BuildConfig));
                var facts = ClientEnvironmentFacts.From(environment);
                var transport = new ServiceCoreTransport(InstallVerifier.ProgramFilesLayout(), BuildVersion.Current);
                var client = new CoreClient(transport, facts.ApiBase.AbsoluteUri.TrimEnd('/'), facts.Channel, BuildVersion.Current);
                session = new SessionController(client, facts, new CredentialManagerStore(), preferences, new FileDeviceIdentity(directory));
                session.Changed += OnSessionChanged;
                Service.BecameReady += () => _ = ContinueAfterServiceAsync();
            }
            catch (Exception error) when (error is IOException or UnauthorizedAccessException or Shared.Env.ClientEnvironmentException)
            {
                ConfigProblem = "构建配置无法读取或无效（" + error.Message + "）。请重新安装。";
            }
        }

        SelectCommand = new AsyncRelay(this, async id => await SelectNodeAsync((string)id!), _ => session is { Busy: false });
        ConnectCommand = new AsyncRelay(this, async _ => await session!.ConnectAsync(), _ => session?.Connection.CanConnect == true);
        DisconnectCommand = new AsyncRelay(this, async _ => await session!.DisconnectAsync(), _ => session is { Busy: false });
        CancelCommand = new AsyncRelay(this, async _ => await session!.CancelOperationAsync(), _ => session is { Busy: true });
        RefreshCommand = new AsyncRelay(this, async _ => await RefreshNowAsync(), _ => session is { Busy: false });
        SignOutCommand = new AsyncRelay(this, async _ => await SignOutAsync(), _ => session is { Busy: false });
        ExitCommand = new RelayCommand(_ => ExitRequested?.Invoke(), _ => true);
        CopyDiagnosticsCommand = new RelayCommand(_ => CopyDiagnostics(), _ => true);
        OpenPlansCommand = new RelayCommand(_ => Open(session?.PlansUrl), _ => session is not null);
        OpenRegisterCommand = new RelayCommand(_ => Open(session?.RegisterUrl), _ => session is not null);
        OpenDashboardCommand = new RelayCommand(_ => Open(session?.DashboardUrl), _ => session is not null);
        OpenOrdersCommand = new RelayCommand(_ => Open(session?.OrdersUrl), _ => session is not null);
    }

    public event PropertyChangedEventHandler? PropertyChanged;

    /// <summary>Asks the user a yes/no question (set by the window; the default refuses, never assumes consent).</summary>
    public Func<string, bool> Confirm { get; set; } = _ => false;

    /// <summary>The user asked to quit (button, tray menu). The application stops the proxy and exits.</summary>
    public event Action? ExitRequested;

    /// <summary>Connection state changed: the tray icon follows it.</summary>
    public event Action? StateChanged;

    public ServicePanel Service { get; }
    public string? ConfigProblem { get; }

    public PlatformReport Report { get; } = PlatformReport.Capture("AsterLink.App");
    public IReadOnlyList<SelfCheckResult> CheckResults { get; } = FoundationSelfCheck.Run();

    // ---- commands ----------------------------------------------------------------------------

    public ICommand SelectCommand { get; }
    public ICommand ConnectCommand { get; }
    public ICommand DisconnectCommand { get; }
    public ICommand CancelCommand { get; }
    public ICommand RefreshCommand { get; }
    public ICommand SignOutCommand { get; }
    public ICommand ExitCommand { get; }
    public ICommand CopyDiagnosticsCommand { get; }
    public ICommand OpenPlansCommand { get; }
    public ICommand OpenRegisterCommand { get; }
    public ICommand OpenDashboardCommand { get; }
    public ICommand OpenOrdersCommand { get; }

    // ---- life cycle --------------------------------------------------------------------------

    /// <summary>Look at the service first; only a ready service is asked to start a Core.</summary>
    public async Task StartAsync()
    {
        if (session is null) { await Service.RefreshAsync(); return; }
        await Service.RefreshAsync();
        if (Service.IsReady) await session.LaunchAsync();
        else session.ServiceUnavailable(Service.Message);
    }

    private async Task ContinueAfterServiceAsync()
    {
        if (session is null) return;
        // The first start-up is StartAsync's own job; this only resumes one that found the service missing.
        await session.ServiceBecameAvailableAsync();
    }

    public async Task ShutdownAsync()
    {
        if (session is not null) await session.ShutdownAsync();
    }

    /// <summary>Window became active or Windows resumed from sleep: nothing cached is trusted, so re-sync the catalog and authorization (rate-limited).</summary>
    public async Task SyncAsync(bool force)
    {
        if (session is not { Phase: AppPhase.SignedIn, Busy: false }) return;
        var now = DateTimeOffset.UtcNow;
        if (!force && now - lastRefresh < ActivationRefreshGap) return;
        lastRefresh = now;
        await session.RefreshAsync();
    }

    private async Task RefreshNowAsync()
    {
        lastRefresh = DateTimeOffset.UtcNow;
        localMessage = null;
        if (session is not null) await session.RefreshAsync();
    }

    public async Task SignInAsync(string password)
    {
        if (session is null) return;
        localMessage = null;
        await session.SignInAsync(Email, password, RememberPassword);
    }

    private async Task SignOutAsync()
    {
        if (session is null) return;
        if (!Confirm("退出登录会断开连接，并清除已保存的密码。继续吗？")) return;
        await session.SignOutAsync();
        page = AppPage.Connect;
        Raise(string.Empty);
    }

    private async Task SelectNodeAsync(string nodeId)
    {
        if (session is null) return;
        var state = session.Snapshot.State;
        if (state is "connected")
        {
            if (nodeId == session.Snapshot.SelectedNodeId) return;
            if (!Confirm("更换线路会先断开当前连接，并结算本次已用流量。继续吗？")) return;
            await session.DisconnectAsync();
            if (session.Snapshot.State == "connected") return;
        }
        await session.SelectAsync(nodeId);
    }

    // ---- session mirror ----------------------------------------------------------------------

    private string lastState = "";

    private void OnSessionChanged()
    {
        var latest = BuildRows();
        if (!rows.SequenceEqual(latest)) rows = latest;
        Raise(string.Empty);
        CommandManager.InvalidateRequerySuggested();
        var state = session!.Snapshot.State + "/" + session.Phase;
        if (state != lastState)
        {
            lastState = state;
            StateChanged?.Invoke();
        }
    }

    private IReadOnlyList<NodeRow> BuildRows() => session is null ? [] : NodeList.Build(session.Snapshot, search, filter);

    private CoreSnapshot Snap => session?.Snapshot ?? SessionController.EmptySnapshot;

    // ---- pages -------------------------------------------------------------------------------

    public bool PageConnect { get => page == AppPage.Connect; set { if (value) SetPage(AppPage.Connect); } }
    public bool PageNodes { get => page == AppPage.Nodes; set { if (value) SetPage(AppPage.Nodes); } }
    public bool PageSettings { get => page == AppPage.Settings; set { if (value) SetPage(AppPage.Settings); } }

    internal void PreviewPage(AppPage target) => SetPage(target);

    private void SetPage(AppPage target)
    {
        if (page == target) return;
        page = target;
        Raise(string.Empty);
    }

    // ---- login page --------------------------------------------------------------------------

    public bool IsSignedIn => session?.Phase == AppPhase.SignedIn;
    public bool ShowLogin => !IsSignedIn;
    public bool IsStarting => session is { Phase: AppPhase.Starting };
    public bool IsSigningIn => session is { Phase: AppPhase.SigningIn };
    public bool LoginEnabled => session is not null && !IsStarting && !IsSigningIn && Service.IsReady;
    public bool ShowServiceCard => !Service.IsReady && ConfigProblem is null;
    public string LoginButtonText => IsSigningIn ? "正在登录…" : "登录";
    public string StartingText => IsStarting ? "正在启动…" : "";
    public bool HasConfigProblem => ConfigProblem is not null;

    public string Email
    {
        get => session?.Email ?? "";
        set { if (session is not null && session.Email != value) { session.Email = value; Raise(); } }
    }

    public bool RememberPassword
    {
        get => session?.RememberPassword ?? false;
        set { if (session is not null && session.RememberPassword != value) { session.RememberPassword = value; Raise(); } }
    }

    /// <summary>The one line the user needs after a failed or refused action (business refusal, service problem, vault warning).</summary>
    public string LoginMessage => ConfigProblem ?? session?.Message ?? (session?.ServiceProblem is { } p && Service.IsReady ? p : "");
    public bool HasLoginMessage => LoginMessage.Length > 0;
    public string LoginWarning => session?.Warning ?? "";
    public bool HasLoginWarning => LoginWarning.Length > 0;

    // ---- header and sidebar ------------------------------------------------------------------

    public string UserEmail => Snap.User?.Email ?? "";
    public bool IsAdmin => Snap.User?.Role == "admin";
    public bool Busy => session?.Busy == true;
    public string Banner => localMessage ?? session?.Message ?? "";
    public bool HasBanner => Banner.Length > 0;
    public string SidebarStatus => ConnStatus;
    public Brush SidebarStatusBrush => ConnBrush;

    // ---- connect page ------------------------------------------------------------------------

    private ConnectionView Connection => session?.Connection ?? ConnectionView.From(SessionController.EmptySnapshot, false);

    private SessionActivity Activity => session?.Activity ?? SessionActivity.None;

    public string ConnStatus => Activity switch
    {
        SessionActivity.Connecting => "正在连接…",
        SessionActivity.Disconnecting => "正在断开…",
        _ => Connection.StatusText,
    };
    public string ConnDetail => Activity switch
    {
        SessionActivity.Connecting => "正在校验线路、建立隧道并验证转发，最长约 90 秒",
        SessionActivity.Disconnecting => "正在关闭数据通路并结算本次流量",
        _ => Connection.Detail,
    };
    public bool HasConnDetail => ConnDetail.Length > 0;
    public Brush ConnBrush => ToneBrush(Activity != SessionActivity.None ? Tone.Busy : Connection.Tone);
    public bool ShowConnect => Connection.ShowConnect && !Busy;
    public bool ShowDisconnect => Connection.ShowDisconnect && Activity != SessionActivity.Disconnecting;
    public bool ShowProgress => Connection.ShowProgress || Busy;
    public bool ShowCancel => Activity == SessionActivity.Connecting;
    public bool CanConnect => Connection.CanConnect;
    public bool ShowDisabledReason => Connection.ShowConnect && !Busy && Connection.DisabledReason.Length > 0;
    public string DisabledReason => Connection.DisabledReason;

    public string SelectedNodeText
    {
        get
        {
            var node = Snap.Nodes.FirstOrDefault(n => n.Id == Snap.SelectedNodeId);
            return node is null ? "尚未选择线路" : $"{node.Name} · {NodeRow.LineLabelOf(node.LineType)} · 流量 {Formatting.Rate(node.RatePermille)}";
        }
    }

    public string SelectedNodeRegion => Snap.Nodes.FirstOrDefault(n => n.Id == Snap.SelectedNodeId)?.Region ?? "";
    public string SelectedNodeMetering => Snap.Nodes.FirstOrDefault(n => n.Id == Snap.SelectedNodeId) is { } node ? NodeRow.MeteringLabelOf(node.MeteringSource) : "";

    // plan card
    public bool HasPlan => Snap.Entitlement?.AccountAuthorized == true;
    public bool NoPlan => !HasPlan;
    public string PlanName => Snap.Entitlement is { PlanName.Length: > 0 } e ? e.PlanName : "套餐";
    public string PlanExpires
    {
        get
        {
            var expires = Snap.Entitlement?.SubscriptionExpiresAt;
            var days = Formatting.DaysLeft(expires, DateTimeOffset.UtcNow);
            return "到期：" + Formatting.Date(expires) + (days is { } d ? $"（剩余 {d} 天）" : "");
        }
    }
    public double PlanUsedPercent => Snap.Entitlement is { } e ? Formatting.UsedFraction(e.TotalBytes, e.RemainingBytes) * 100 : 0;
    public string PlanRemaining => Snap.Entitlement is { } e
        ? $"剩余 {Formatting.Bytes(e.RemainingBytes)} / 共 {Formatting.Bytes(e.TotalBytes)}（1 GiB = 2³⁰ 字节，以服务器确认值为准）"
        : "";
    public string NoPlanText => Snap.Entitlement?.ReasonMessage is { Length: > 0 } text ? text : "暂无可用套餐，请前往网站购买或续费";

    // metering
    public bool ShowMetering => Snap.Metering is { } m && (Snap.State == "connected" || m.UploadBytes + m.DownloadBytes > 0);
    public string UpSpeed => "↑ " + Formatting.Speed(Snap.Metering?.UploadBytesPerSecond ?? 0);
    public string DownSpeed => "↓ " + Formatting.Speed(Snap.Metering?.DownloadBytesPerSecond ?? 0);
    public string RateText => "线路倍率 " + Formatting.Rate(Snap.Metering?.RatePermille ?? 0);
    public string SessionTotals => Snap.Metering is { } m
        ? $"本次登录经线路传输：上行 {Formatting.Bytes(m.UploadBytes)} · 下行 {Formatting.Bytes(m.DownloadBytes)}"
        : "";
    public string RemainingEstimate => Snap.Metering is { } m
        ? $"预估剩余套餐流量 {Formatting.Bytes(m.EstimatedRemainingBytes)}（服务器已确认 {Formatting.Bytes(m.ConfirmedRemainingBytes)}{(m.Pending ? "，有批次待服务器确认" : "")}）"
        : "";
    public bool ShowNodeMeteredNote => Snap.Metering?.Source == "node_reported";
    public PointCollection UpPoints => Chart(s => s.Upload);
    public PointCollection DownPoints => Chart(s => s.Download);

    private PointCollection Chart(Func<MeterSample, long> pick)
    {
        var history = Snap.Metering?.History ?? [];
        var points = new PointCollection();
        if (history.Count < 2) return points;
        var samples = history.Count > 120 ? history.Skip(history.Count - 120).ToList() : history.ToList();
        // One shared scale for both directions so the two lines compare honestly.
        var max = Math.Max(1, samples.Max(s => Math.Max(s.Upload, s.Download)));
        for (var i = 0; i < samples.Count; i++)
        {
            var x = (double)i / (samples.Count - 1) * ChartWidth;
            var y = ChartHeight - (double)pick(samples[i]) / max * (ChartHeight - 2) - 1;
            points.Add(new Point(x, y));
        }
        return points;
    }

    // ---- nodes page --------------------------------------------------------------------------

    public IReadOnlyList<NodeRow> Rows => rows;
    public bool ShowNodesEmpty => rows.Count == 0 && NodesEmptyText.Length > 0;
    public string NodesEmptyText => NodeList.EmptyText(Snap, search.Trim().Length > 0 || filter != LineFilter.All);
    public string NodesHint => Snap.State == "connected" ? "已连接。更换线路会先断开并结算本次流量。" : "";
    public bool HasNodesHint => NodesHint.Length > 0;

    public string Search
    {
        get => search;
        set { if (search == value) return; search = value; rows = BuildRows(); Raise(string.Empty); }
    }

    public bool FilterAll { get => filter == LineFilter.All; set { if (value) SetFilter(LineFilter.All); } }
    public bool FilterDedicated { get => filter == LineFilter.Dedicated; set { if (value) SetFilter(LineFilter.Dedicated); } }
    public bool FilterDirect { get => filter == LineFilter.Direct; set { if (value) SetFilter(LineFilter.Direct); } }

    private void SetFilter(LineFilter value)
    {
        if (filter == value) return;
        filter = value;
        rows = BuildRows();
        Raise(string.Empty);
    }

    // ---- settings page -----------------------------------------------------------------------

    public bool ThemeSystem { get => theme == "system"; set { if (value) SetTheme("system"); } }
    public bool ThemeLight { get => theme == "light"; set { if (value) SetTheme("light"); } }
    public bool ThemeDark { get => theme == "dark"; set { if (value) SetTheme("dark"); } }

    public void SetTheme(string value)
    {
        if (theme == value) return;
        theme = value;
        preferences.Theme = value;
        ThemeManager.Apply(value);
        Raise(string.Empty);
    }

    public void ApplyTheme() => ThemeManager.Apply(theme);

    public bool TrayNoticeShown
    {
        get => preferences.TrayNoticeShown;
        set => preferences.TrayNoticeShown = value;
    }

    public string AccountSummary => IsSignedIn
        ? $"{UserEmail}{(IsAdmin ? "（管理员账号）" : "")}\n" + (HasPlan ? $"{PlanName} · {PlanExpires}" : NoPlanText)
        : "";
    public bool CanChangeService => !IsSignedIn;
    public string VersionText => $"应用 {BuildVersion.Current} · 渠道 {environment?.AppEnv ?? "未配置"} · 内核 {(Snap.CoreVersion.Length == 0 ? "—" : Snap.CoreVersion)}";
    public string ServerText => "服务器 " + (environment?.ApiBaseUrl.Host ?? "—");

    public IReadOnlyList<FactRow> Facts =>
    [
        new("操作系统", Report.OsDescription),
        new("系统架构", Report.OsArchitecture),
        new("进程架构", Report.ProcessArchitecture),
        new("运行时", Report.RuntimeDescription),
        new("原生 Windows x64", Report.IsNativeWindowsX64 ? "是" : "否"),
    ];

    public IReadOnlyList<CheckRow> Checks => [.. CheckResults.Select(c => new CheckRow(c.Name, c.Passed))];

    private void CopyDiagnostics()
    {
        var text = DiagnosticsReport.Build(BuildVersion.Current, environment?.AppEnv ?? "unconfigured", environment?.ApiBaseUrl.Host ?? "-",
            Report.OsDescription, Snap, Service.State);
        try
        {
            Clipboard.SetText(text);
            localMessage = "脱敏诊断信息已复制到剪贴板";
        }
        catch (System.Runtime.InteropServices.COMException)
        {
            localMessage = "剪贴板正被其他程序占用，请稍后重试";
        }
        Raise(string.Empty);
    }

    private static void Open(Uri? url)
    {
        if (url is null || url.Scheme is not ("https" or "http")) return;
        try { Process.Start(new ProcessStartInfo(url.AbsoluteUri) { UseShellExecute = true }); }
        catch (System.ComponentModel.Win32Exception) { }
    }

    // ---- tray support ------------------------------------------------------------------------

    public string State => Snap.State;
    public bool IsLive => IsSignedIn && (Snap.State is "connected" or "connecting" or "preparing" || Activity == SessionActivity.Connecting);

    /// <summary>For the design preview: the service card in a given state.</summary>
    internal void PreviewServiceState(ServiceProbeResult result) => Service.ApplyPreview(result);
    public string TrayText => IsSignedIn ? "AsterLink · " + Connection.StatusText : "AsterLink · 未登录";

    // ---- plumbing ----------------------------------------------------------------------------

    private static Brush ToneBrush(Tone tone) => (Brush)Application.Current.Resources[tone switch
    {
        Tone.Good => "PassBrush",
        Tone.Bad => "FailBrush",
        Tone.Busy => "WarnBrush",
        _ => "MutedBrush",
    }];

    internal void ReportFailure(Exception error)
    {
        // Never include the exception message verbatim in anything that could carry a secret: type name only.
        localMessage = "操作失败（" + error.GetType().Name + "），请重试";
        Raise(string.Empty);
    }

    private void Raise([CallerMemberName] string? name = null) => PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}

public sealed class RelayCommand(Action<object?> execute, Func<object?, bool> canExecute) : ICommand
{
    public event EventHandler? CanExecuteChanged
    {
        add => CommandManager.RequerySuggested += value;
        remove => CommandManager.RequerySuggested -= value;
    }

    public bool CanExecute(object? parameter) => canExecute(parameter);
    public void Execute(object? parameter) => execute(parameter);
}

/// <summary>An async command that cannot crash the app: a failure becomes a short message.</summary>
public sealed class AsyncRelay(AppViewModel owner, Func<object?, Task> execute, Func<object?, bool> canExecute) : ICommand
{
    public event EventHandler? CanExecuteChanged
    {
        add => CommandManager.RequerySuggested += value;
        remove => CommandManager.RequerySuggested -= value;
    }

    public bool CanExecute(object? parameter) => canExecute(parameter);

    public async void Execute(object? parameter)
    {
        try { await execute(parameter); }
        catch (Exception error) { owner.ReportFailure(error); }
    }
}

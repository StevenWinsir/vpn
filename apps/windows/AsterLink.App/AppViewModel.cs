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
    private const double ChartWidth = 300;
    private const double ChartHeight = 64;
    private const int ChartSamples = 60;
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
    private bool diagnosticsCopied;
    private System.Windows.Threading.DispatcherTimer? copiedReset;

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
            ConfigProblem = "请从开始菜单打开已安装的 AsterLink。";
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
                ConfigProblem = "应用安装不完整，请从官网重新下载安装。";
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
        OpenForgotPasswordCommand = new RelayCommand(_ => Open(session?.ForgotPasswordUrl), _ => session is not null);
        OpenDashboardCommand = new RelayCommand(_ => Open(session?.DashboardUrl), _ => session is not null);
        OpenOrdersCommand = new RelayCommand(_ => Open(session?.OrdersUrl), _ => session is not null);
        OpenWebsiteCommand = new RelayCommand(_ => Open(session?.WebsiteBase), _ => session is not null);
        ShowLinesCommand = new RelayCommand(_ => SetPage(AppPage.Nodes), _ => true);
        ClearFilterCommand = new RelayCommand(_ => { search = ""; filter = LineFilter.All; rows = BuildRows(); Raise(string.Empty); }, _ => true);
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
    public ICommand OpenForgotPasswordCommand { get; }
    public ICommand OpenDashboardCommand { get; }
    public ICommand OpenOrdersCommand { get; }
    public ICommand OpenWebsiteCommand { get; }
    public ICommand ShowLinesCommand { get; }
    public ICommand ClearFilterCommand { get; }

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
        if (!Confirm("退出登录会断开连接，并清除本机保存的密码。继续吗？")) return;
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
            var name = session.Snapshot.Nodes.FirstOrDefault(n => n.Id == nodeId)?.Name ?? "这条线路";
            if (!Confirm($"更换到“{name}”会断开当前连接，更换后需要重新连接。继续吗？")) return;
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

    public string PageTitle => page switch { AppPage.Nodes => "线路", AppPage.Settings => "账户", _ => "连接" };
    public string PageSubtitle => page == AppPage.Nodes ? NodesSubtitle : "";

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
    public bool LoginBusy => IsStarting || IsSigningIn;
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
    public Brush SidebarStatusBackground => Resource(ToneOf() switch
    {
        Tone.Good => "BrandSoftBrush",
        Tone.Bad => "DangerBackgroundBrush",
        Tone.Busy => "WarnBackgroundBrush",
        _ => "SurfaceMutedBrush",
    });

    /// <summary>Whether Windows allows animation (Settings › Accessibility › Visual effects): off means the connect arc stands still.</summary>
    public bool AnimationsEnabled => SystemParameters.ClientAreaAnimation;

    // ---- connect page ------------------------------------------------------------------------

    private ConnectionView Connection => session?.Connection ?? ConnectionView.From(SessionController.EmptySnapshot, false);

    private SessionActivity Activity => session?.Activity ?? SessionActivity.None;

    private Tone ToneOf() => Activity != SessionActivity.None ? Tone.Busy : Connection.Tone;

    public string ConnStatus => Activity switch
    {
        SessionActivity.Connecting => "正在连接…",
        SessionActivity.Disconnecting => "正在断开…",
        _ => Connection.StatusText,
    };
    public string ConnDetail => Activity switch
    {
        SessionActivity.Connecting => "正在为你建立安全连接，请稍候",
        SessionActivity.Disconnecting => "正在断开当前连接",
        _ when Snap.State == "connected" && SelectedNode is { } node => $"已通过“{node.Name}”为你建立安全连接",
        _ when Connection.ShowProgress => Connection.Detail,
        _ when Connection.Tone == Tone.Bad => "请查看上方提示，或稍后重试",
        _ => "选择线路后，点击按钮即可连接",
    };
    public bool HasConnDetail => ConnDetail.Length > 0;
    public Brush ConnBrush => ToneBrush(ToneOf());
    /// <summary>The status line is the plain text colour while idle and the state colour otherwise.</summary>
    public Brush ConnTitleBrush => ToneOf() == Tone.Idle ? Resource("TextPrimaryBrush") : ConnBrush;
    public bool ShowConnect => Connection.ShowConnect && !Busy;
    public bool ShowDisconnect => Connection.ShowDisconnect && Activity != SessionActivity.Disconnecting;
    /// <summary>Neither action applies (a command is running): the big button shows progress and does nothing.</summary>
    public bool ShowWaitButton => !ShowConnect && !ShowDisconnect;
    public bool ShowProgress => Connection.ShowProgress || Busy;
    public bool ShowCancel => Activity == SessionActivity.Connecting;
    public bool CanConnect => Connection.CanConnect;
    public bool ShowDisabledReason => Connection.ShowConnect && !Busy && Connection.DisabledReason.Length > 0;
    public string DisabledReason => Connection.DisabledReason;

    /// <summary>The look of the connect button: idle, busy, good or bad (Theme/Styles.xaml ConnectButton).</summary>
    public string ConnVisual => ToneOf() switch
    {
        Tone.Good => "good",
        Tone.Busy => "busy",
        Tone.Bad => "bad",
        _ => "idle",
    };
    public string ConnectButtonName => "连接（" + ConnStatus.Replace("…", "", StringComparison.Ordinal) + "）";
    public string DisconnectButtonName => "断开（" + ConnStatus.Replace("…", "", StringComparison.Ordinal) + "）";
    public string WaitButtonName => ConnStatus;

    // One notice above the page, with at most one next step: why Connect is unavailable, else what went wrong last.
    public string BlockerText => ShowDisabledReason ? DisabledReason : "";
    public bool BlockerIsPlan => !HasPlan;
    public bool BlockerIsLine => HasPlan && Snap.Nodes.Count > 0;
    public bool BlockerIsRefresh => HasPlan && Snap.Nodes.Count == 0;
    public string BlockerKind => BlockerIsPlan ? "danger" : "warning";
    public string BlockerAction => BlockerText.Length == 0 ? "" : BlockerIsPlan ? "购买套餐" : BlockerIsLine ? "选择线路" : "刷新";
    public ICommand BlockerCommand => BlockerIsPlan ? OpenPlansCommand : BlockerIsLine ? ShowLinesCommand : RefreshCommand;
    /// <summary>The last refusal or failure from the Core, when nothing above already says it.</summary>
    public string FailureText => Snap.State != "connected" && BlockerText.Length == 0 && !HasBanner && Snap.LastError is { Message.Length: > 0 } e ? e.Message : "";

    // current line card
    private NodeView? SelectedNode => Snap.Nodes.FirstOrDefault(n => n.Id == Snap.SelectedNodeId);
    public bool HasSelectedNode => SelectedNode is not null;
    public bool NoSelectedNode => SelectedNode is null;
    public string SelectedNodeText => SelectedNode?.Name ?? "未选择线路";
    public string SelectedNodeRegion => SelectedNode?.Region ?? "";
    public string SelectedNodeLine => SelectedNode is { } node ? NodeRow.LineLabelOf(node.LineType) : "";
    public bool SelectedNodeDedicated => SelectedNode?.LineType == "dedicated";
    public string SelectedNodeRate => SelectedNode is { } node ? "线路倍率 " + Formatting.Rate(node.RatePermille) : "";
    public string LineCardAction => HasSelectedNode ? "更换" : "选择线路";

    // plan card
    public bool HasPlan => Snap.Entitlement?.AccountAuthorized == true;
    public bool NoPlan => !HasPlan;
    public string PlanName => Snap.Entitlement is { PlanName.Length: > 0 } e ? e.PlanName : "当前套餐";
    public string PlanDays => Formatting.DaysLeft(Snap.Entitlement?.SubscriptionExpiresAt, DateTimeOffset.UtcNow) switch
    {
        null => "",
        0 => "今天到期",
        var d => $"剩余 {d} 天",
    };
    public bool PlanDaysUrgent => Formatting.DaysLeft(Snap.Entitlement?.SubscriptionExpiresAt, DateTimeOffset.UtcNow) is <= 3;
    public string PlanExpires => Snap.Entitlement?.SubscriptionExpiresAt is { } at ? Formatting.Date(at) + " 到期" : "";
    public double PlanUsedPercent => Snap.Entitlement is { } e ? Formatting.UsedFraction(e.TotalBytes, e.RemainingBytes) * 100 : 0;
    public string PlanRemaining => Snap.Entitlement is { } e
        ? $"剩余 {Formatting.Bytes(e.RemainingBytes)} / {Formatting.Bytes(e.TotalBytes)}"
        : "";
    public string PlanEstimate => Snap.Metering is { } m && HasPlan
        ? "预估剩余 " + Formatting.Bytes(m.EstimatedRemainingBytes) + (m.Pending ? " · 正在同步用量" : "")
        : "";
    public string NoPlanText => Snap.Entitlement?.ReasonMessage is { Length: > 0 } text ? text : "购买或续费后即可连接";
    public string PlanAction => HasPlan ? "续费" : "购买套餐";

    // rate card
    public bool ShowMetering => Snap.Metering is { } m && (Snap.State == "connected" || m.UploadBytes + m.DownloadBytes > 0);
    public bool NoMetering => !ShowMetering;
    public string UpSpeed => Formatting.Speed(Snap.Metering?.UploadBytesPerSecond ?? 0);
    public string DownSpeed => Formatting.Speed(Snap.Metering?.DownloadBytesPerSecond ?? 0);
    public string SessionTotals => Snap.Metering is { } m
        ? $"本次已用 {Formatting.Bytes(m.UploadBytes + m.DownloadBytes)} · 按倍率计 {Formatting.Bytes(Formatting.BilledBytes(m.UploadBytes + m.DownloadBytes, m.RatePermille))}"
        : "";
    public Geometry DownGeometry => Chart(s => s.Download, closed: false);
    public Geometry DownArea => Chart(s => s.Download, closed: true);
    public Geometry UpGeometry => Chart(s => s.Upload, closed: false);

    /// <summary>
    /// One rate line of the last 60 samples on a fixed 300 x 64 canvas. Both lines share one scale so they compare honestly,
    /// and every geometry carries the two canvas corners as unstroked points, so the views can stretch them all alike.
    /// </summary>
    private Geometry Chart(Func<MeterSample, long> pick, bool closed)
    {
        var history = Snap.Metering?.History ?? [];
        var geometry = new StreamGeometry();
        using (var context = geometry.Open())
        {
            context.BeginFigure(new Point(0, 0), false, false);
            context.LineTo(new Point(ChartWidth, ChartHeight), false, false);
            if (history.Count >= 2)
            {
                var samples = history.Count > ChartSamples ? history.Skip(history.Count - ChartSamples).ToList() : history.ToList();
                var max = Math.Max(1, samples.Max(s => Math.Max(s.Upload, s.Download)));
                var points = samples.Select((sample, i) => new Point(
                    (double)i / (samples.Count - 1) * ChartWidth,
                    ChartHeight - (double)pick(sample) / max * (ChartHeight - 4) - 2)).ToList();
                context.BeginFigure(closed ? new Point(0, ChartHeight) : points[0], closed, closed);
                if (closed) context.LineTo(points[0], false, true);
                foreach (var point in points.Skip(1)) context.LineTo(point, true, true);
                if (closed) context.LineTo(new Point(ChartWidth, ChartHeight), false, true);
            }
        }
        geometry.Freeze();
        return geometry;
    }

    // ---- nodes page --------------------------------------------------------------------------

    public IReadOnlyList<NodeRow> Rows => rows;
    public IReadOnlyList<RegionGroup> Groups => NodeList.Group(rows);
    public bool ShowNodesEmpty => rows.Count == 0 && NodesEmptyText.Length > 0;
    private bool Filtered => search.Trim().Length > 0 || filter != LineFilter.All;
    public string NodesEmptyText => NodeList.EmptyText(Snap, Filtered);
    public bool NodesEmptyBuy => ShowNodesEmpty && !HasPlan;
    public bool NodesEmptyClear => ShowNodesEmpty && HasPlan && Snap.Nodes.Count > 0 && Filtered;
    public bool NodesEmptyRefresh => ShowNodesEmpty && HasPlan && Snap.Nodes.Count == 0;
    public string NodesSubtitle => Snap.Nodes.Count == 0 ? "" : $"共 {Snap.Nodes.Count} 条线路";
    public string NodesHint => Snap.State == "connected" ? "已连接。更换线路会先断开当前连接。" : "";
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

    /// <summary>Windows changed its app colour mode: only "跟随系统" follows it.</summary>
    public void FollowSystemTheme()
    {
        if (theme == "system" && ThemeManager.SystemPrefersDark() != ThemeManager.IsDark) ThemeManager.Apply(theme);
    }

    public bool TrayNoticeShown
    {
        get => preferences.TrayNoticeShown;
        set => preferences.TrayNoticeShown = value;
    }

    public string AccountInitial => UserEmail.Length > 0 ? UserEmail[..1].ToUpperInvariant() : "A";
    public string AccountPlanSummary
    {
        get
        {
            if (Snap.Entitlement is not { AccountAuthorized: true } e) return "暂无有效套餐";
            var parts = new List<string> { PlanName };
            if (e.SubscriptionExpiresAt is { } at) parts.Add(Formatting.Date(at) + " 到期");
            if (e.TotalBytes > 0) parts.Add("剩余 " + Formatting.Bytes(e.RemainingBytes));
            return string.Join(" · ", parts);
        }
    }
    public string RenewText => HasPlan ? "续费或升级" : "购买套餐";
    public bool CanChangeService => !IsSignedIn;

    /// <summary>"版本 x.y.z" only (D8): no engine, channel or server. Development builds add a neutral marker.</summary>
    public string VersionText => "版本 " + BuildVersion.Current;
    public bool IsDevelopmentBuild => environment is null || environment.AppEnv == "development";
    public string DevelopmentMarker => IsDevelopmentBuild ? "开发版" : ""; // copy-guard: D8 neutral marker, development builds only

    public string CopyDiagnosticsText => diagnosticsCopied ? "已复制" : "复制诊断信息";

    /// <summary>"Windows 11 (10.0.26100)": what support needs, without runtime or architecture details.</summary>
    public static string OsVersion
    {
        get
        {
            var version = Environment.OSVersion.Version;
            var name = version.Major == 10 && version.Build >= 22000 ? "Windows 11" : version.Major == 10 ? "Windows 10" : "Windows";
            return $"{name} ({version.Major}.{version.Minor}.{version.Build})";
        }
    }

    private void CopyDiagnostics()
    {
        var text = DiagnosticsReport.Build(BuildVersion.Current, OsVersion, IsSignedIn ? ConnStatus : "未登录", Snap, DateTimeOffset.Now);
        try
        {
            Clipboard.SetText(text);
            diagnosticsCopied = true;
            copiedReset ??= new System.Windows.Threading.DispatcherTimer { Interval = TimeSpan.FromSeconds(2) };
            copiedReset.Stop();
            copiedReset.Tick -= ResetCopied;
            copiedReset.Tick += ResetCopied;
            copiedReset.Start();
        }
        catch (System.Runtime.InteropServices.COMException)
        {
            localMessage = "剪贴板正被其他程序占用，请稍后重试";
        }
        Raise(string.Empty);
    }

    private void ResetCopied(object? sender, EventArgs e)
    {
        copiedReset?.Stop();
        diagnosticsCopied = false;
        Raise(nameof(CopyDiagnosticsText));
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
    public string TrayText => "AsterLink · " + TrayStatus;

    /// <summary>The first, disabled line of the tray menu: "已连接 · 东京专线 01", "未连接" or "未登录".</summary>
    public string TrayStatus => !IsSignedIn ? (IsStarting ? "正在启动…" : "未登录")
        : Snap.State == "connected" && SelectedNode is { } node ? ConnStatus + " · " + node.Name
        : ConnStatus;

    // ---- plumbing ----------------------------------------------------------------------------

    private static Brush ToneBrush(Tone tone) => Resource(tone switch
    {
        Tone.Good => "BrandBrush",
        Tone.Bad => "DangerTextBrush",
        Tone.Busy => "WarnTextBrush",
        _ => "TextTertiaryBrush",
    });

    private static Brush Resource(string key) => (Brush)Application.Current.Resources[key];

    internal void ReportFailure(Exception error)
    {
        // Never show the exception: it can carry internals (and, in theory, a secret). One plain sentence.
        localMessage = "操作失败，请重试";
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

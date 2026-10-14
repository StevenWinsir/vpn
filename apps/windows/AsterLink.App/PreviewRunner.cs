using System.IO;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using System.Windows.Threading;
using AsterLink.Platform;
using AsterLink.Shared.Client;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;
using AsterLink.Shared.Snapshot;

namespace AsterLink.App;

/// <summary>
/// <c>AsterLink.exe --preview &lt;dir&gt;</c>: renders every page in the states the handoff asks to see (empty states, errors,
/// connected, dark, 200 % scale) to PNG files, using a scripted Core. It never touches the real service, the credential
/// vault or the network, and the demo data is made up. CI keeps the PNGs as design evidence; the user can run it too.
/// </summary>
public static class PreviewRunner
{
    private sealed class ScriptedTransport : ICoreTransport
    {
        public CoreSnapshot Current { get; set; } = new() { State = "signed_out" };

        public Task StartAsync(CancellationToken cancellationToken = default) => Task.CompletedTask;
        public Task StopAsync() => Task.CompletedTask;

        public Task<IpcResponse> SendAsync(string command, ulong generation, JsonElement? payload, CancellationToken cancellationToken = default)
        {
            var node = JsonSerializer.SerializeToNode(Current, SnapshotJson.Options)!.AsObject();
            node["action_error"] = null;
            return Task.FromResult(new IpcResponse { RequestId = "p", ProtocolVersion = IpcProtocol.Version, Ok = true, Result = JsonSerializer.SerializeToElement(node) });
        }
    }

    private sealed class MemoryPreferences(string theme) : ILoginPreferences
    {
        public string? RememberedEmail { get; set; }
        public string Theme { get; set; } = theme;
        public bool TrayNoticeShown { get; set; } = true;
    }

    private sealed class NoVault : ICredentialStore
    {
        public void Save(string account, string password) { }
        public string? Read(string account) => null;
        public void Delete(string account) { }
    }

    private sealed class DemoDevice : IDeviceIdentity
    {
        public string DeviceId() => "00000000-0000-4000-8000-000000000000";
    }

    private const string NodeTokyo = "11111111-1111-4111-8111-111111111111";
    private const string NodeSingapore = "22222222-2222-4222-8222-222222222222";
    private const string NodeHongKong = "33333333-3333-4333-8333-333333333333";
    private const string NodeLosAngeles = "44444444-4444-4444-8444-444444444444";

    public static async Task<int> RunAsync(string directory)
    {
        Directory.CreateDirectory(directory);
        var written = new List<string>();
        var environment = Shared.Env.ClientEnvironment.Parse(
            "APP_ENV=development\nAPI_BASE_URL=https://demo.example.test/api/v1/client\nWEBSITE_BASE_URL=https://site.example.test\n");

        async Task Scenario(string name, string theme, Func<ScriptedTransport, CoreSnapshot?> snapshot, AppPage page = AppPage.Connect,
            double height = 680, double scale = 1, Action<AppViewModel>? tweak = null, double width = 1000)
        {
            var transport = new ScriptedTransport();
            var facts = ClientEnvironmentFacts.From(environment);
            var client = new CoreClient(transport, facts.ApiBase.AbsoluteUri.TrimEnd('/'), facts.Channel, "0.0.0");
            var preferences = new MemoryPreferences(theme);
            var session = new SessionController(client, facts, new NoVault(), preferences, new DemoDevice(), TimeSpan.FromHours(1), TimeSpan.FromHours(1));
            ThemeManager.Apply(theme);
            var model = new AppViewModel(new PreviewParts(session, environment, preferences));
            model.PreviewServiceState(new ServiceProbeResult
            {
                Connected = true, Health = ServiceHealthState.Ready, Action = RecommendedAction.None, Message = "特权服务已就绪。",
                ServiceVersion = "0.0.0", CoreVersion = "0.0.0", Channel = "development", SnapshotState = "signed_out",
            });

            await session.LaunchAsync();
            if (snapshot(transport) is { } signedIn)
            {
                transport.Current = signedIn;
                await session.SignInAsync("demo@example.com", "preview-only", remember: false);
            }
            tweak?.Invoke(model);
            model.PreviewPage(page);

            var window = new MainWindow(model) { Width = width, Height = height, WindowStartupLocation = WindowStartupLocation.Manual, Left = 0, Top = 0, ShowInTaskbar = false };
            window.Show();
            await Dispatcher.Yield(DispatcherPriority.ApplicationIdle);
            window.UpdateLayout();
            await Dispatcher.Yield(DispatcherPriority.ApplicationIdle);

            var pixelsWide = (int)Math.Ceiling(window.ActualWidth * scale);
            var pixelsHigh = (int)Math.Ceiling(window.ActualHeight * scale);
            var bitmap = new RenderTargetBitmap(pixelsWide, pixelsHigh, 96 * scale, 96 * scale, PixelFormats.Pbgra32);
            bitmap.Render(window);
            var encoder = new PngBitmapEncoder();
            encoder.Frames.Add(BitmapFrame.Create(bitmap));
            var path = Path.Combine(directory, name + ".png");
            await using (var stream = File.Create(path)) encoder.Save(stream);
            written.Add(name);
            window.Close();
        }

        // Login and service states (not signed in).
        await Scenario("01-login", "light", _ => null);
        await Scenario("02-login-dark", "dark", _ => null);
        await Scenario("03-login-service-missing", "light", _ => null, tweak: m => m.PreviewServiceState(ServiceProbe(ScmState.NotInstalled, null)));
        await Scenario("04-login-needs-repair", "light", _ => null, tweak: m => m.PreviewServiceState(ServiceProbe(ScmState.Running, ServiceErrors.IntegrityFailed)));

        // Connect page.
        await Scenario("10-connect-ready", "light", _ => Demo("ready", selected: NodeTokyo, configReady: true));
        await Scenario("11-connect-no-line-selected", "light", _ => Demo("ready"));
        await Scenario("12-connect-connected", "light", _ => Demo("connected", selected: NodeTokyo, configReady: true, metering: true));
        await Scenario("13-connect-connected-dark", "dark", _ => Demo("connected", selected: NodeSingapore, configReady: true, metering: true, nodeMetered: true));
        await Scenario("14-connect-connected-200pct", "light", _ => Demo("connected", selected: NodeTokyo, configReady: true, metering: true), scale: 2);
        await Scenario("15-connect-admin-no-plan", "light", _ => Demo("blocked", authorized: false, role: "admin", reason: "paid_vip_required"));
        await Scenario("16-connect-expired", "light", _ => Demo("blocked", authorized: false, reason: "subscription_expired"));
        await Scenario("17-connect-quota-exhausted", "light", _ => Demo("blocked", authorized: false, reason: "quota_exhausted", remaining: 0));
        await Scenario("18-connect-error", "light", _ => Demo("error", selected: NodeTokyo, configReady: true, error: ("tunnel_failed", "隧道启动失败，请稍后重试或联系管理员")));
        await Scenario("19-connect-min-size", "light", _ => Demo("connected", selected: NodeTokyo, configReady: true, metering: true), height: 560, width: 800);

        // Lines page.
        await Scenario("20-nodes", "light", _ => Demo("ready", selected: NodeHongKong, configReady: true), AppPage.Nodes);
        await Scenario("21-nodes-dark", "dark", _ => Demo("ready", selected: NodeHongKong, configReady: true), AppPage.Nodes);
        await Scenario("22-nodes-empty-no-plan", "light", _ => Demo("blocked", authorized: false, nodes: false, reason: "paid_vip_required"), AppPage.Nodes);
        await Scenario("23-nodes-empty-admin-has-none", "light", _ => Demo("ready", nodes: false), AppPage.Nodes);
        await Scenario("24-nodes-search-no-match", "light", _ => Demo("ready"), AppPage.Nodes, tweak: m => m.Search = "不存在");
        await Scenario("25-nodes-connected-hint", "light", _ => Demo("connected", selected: NodeTokyo, configReady: true, metering: true), AppPage.Nodes);

        // Account and settings.
        await Scenario("30-settings", "light", _ => Demo("ready", selected: NodeTokyo, configReady: true), AppPage.Settings, height: 1180);
        await Scenario("31-settings-dark", "dark", _ => Demo("connected", selected: NodeTokyo, configReady: true, metering: true), AppPage.Settings, height: 1180);

        ThemeManager.Apply("light");
        File.WriteAllText(Path.Combine(directory, "index.json"), JsonSerializer.Serialize(new { count = written.Count, pages = written }, new JsonSerializerOptions { WriteIndented = true }));
        return written.Count > 0 ? 0 : 1;
    }

    private static ServiceProbeResult ServiceProbe(ScmState scm, string? code)
    {
        var health = ServiceHealthClassifier.Classify(guiInInstalledRoot: true, guiBesideStage: true, scm, code);
        return new ServiceProbeResult { ScmState = scm.ToString(), Health = health.State, Action = health.Action, Message = health.Message, ErrorCode = code };
    }

    private static CoreSnapshot Demo(string state, string selected = "", bool configReady = false, bool metering = false, bool authorized = true,
        string role = "user", string reason = "", long remaining = 147_634_257_920, bool nodes = true, bool nodeMetered = false, (string Code, string Message)? error = null)
    {
        var plan = authorized
            ? new EntitlementView
            {
                PlanName = "尊享月卡", AccountAuthorized = true, CanConnect = selected.Length > 0, Reason = "", ReasonMessage = "",
                TotalBytes = 214_748_364_800, RemainingBytes = remaining, UsedUnits = (214_748_364_800 - remaining) * 1000,
                SubscriptionExpiresAt = DateTimeOffset.UtcNow.AddDays(23), AuthorizationExpiresAt = DateTimeOffset.UtcNow.AddMinutes(2), LeaseRemainingSeconds = 90,
            }
            : new EntitlementView
            {
                AccountAuthorized = false, CanConnect = false, Reason = reason,
                ReasonMessage = reason switch
                {
                    "subscription_expired" => "套餐已到期，请前往网站续费",
                    "quota_exhausted" => "套餐流量已用完，请前往网站续费或升级",
                    _ => "需要有效的付费套餐，请前往网站购买或续费",
                },
            };

        var list = new List<NodeView>();
        if (nodes)
        {
            list.Add(new NodeView { Id = NodeTokyo, Name = "东京专线 01", Region = "日本 · 东京", LineType = "dedicated", RatePermille = 1000 });
            list.Add(new NodeView { Id = NodeSingapore, Name = "新加坡专线 02", Region = "新加坡", LineType = "dedicated", RatePermille = 1000, MeteringSource = "node_reported" });
            list.Add(new NodeView { Id = NodeHongKong, Name = "香港直连 01", Region = "中国香港", LineType = "direct", RatePermille = 500 });
            list.Add(new NodeView { Id = NodeLosAngeles, Name = "洛杉矶直连 01 · 一个特别长的线路名称用来检查换行和裁切", Region = "美国 · 洛杉矶", LineType = "direct", RatePermille = 500 });
        }

        var history = Enumerable.Range(0, 60).Select(i => new MeterSample
        {
            Download = (long)(2_500_000 + 1_800_000 * Math.Sin(i / 6.0) + (i % 7) * 120_000),
            Upload = (long)(180_000 + 90_000 * Math.Sin(i / 4.0 + 1)),
        }).ToList();

        return new CoreSnapshot
        {
            Generation = 3,
            State = state,
            User = new UserView { Id = "u", Email = role == "admin" ? "admin@example.com" : "demo@example.com", Name = "Demo", Role = role },
            Entitlement = plan,
            Nodes = list,
            SelectedNodeId = selected,
            CatalogRevision = new string('a', 64),
            ConfigReady = configReady,
            TunnelHealth = state == "connected" ? "up" : "",
            CoreVersion = "0.0.0",
            LastError = error is { } e ? new ErrorView { Code = e.Code, Message = e.Message } : null,
            Metering = metering
                ? new MeteringView
                {
                    Source = nodeMetered ? "node_reported" : "client_reported",
                    UploadBytes = 18_874_368, DownloadBytes = 432_013_312, LastSequence = 4,
                    UploadBytesPerSecond = history[^1].Upload, DownloadBytesPerSecond = history[^1].Download,
                    ConfirmedUploadBytes = 12_582_912, ConfirmedDownloadBytes = 301_989_888,
                    ConfirmedRemainingBytes = remaining, EstimatedRemainingBytes = remaining - 140_000_000, RatePermille = 1000,
                    Pending = true, History = history,
                }
                : null,
        };
    }
}

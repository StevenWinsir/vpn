using System.IO;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Documents;
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
/// connected, minimum size, light and dark, 200 % scale, the tray menu) to PNG files, using a scripted Core, and writes
/// texts.json with every visible string of each screen for the runtime copy guard (scripts/test-native-windows.py --suite
/// ui). It never touches the real service, the credential vault or the network, and the demo data is made up. CI keeps
/// the PNGs as design evidence; the user can run it too.
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

    /// <summary>One screen to draw: a page in a state. Every scenario is drawn in the light and the dark scheme.</summary>
    private sealed record Scene(string Name, Func<CoreSnapshot?> Snapshot, AppPage Page = AppPage.Connect, double Width = 1000, double Height = 680,
        Action<AppViewModel>? Tweak = null, bool Wide = false);

    /// <summary>The scenarios (aligned with the macOS preview, handoff section 10 round 3), drawn light and dark.</summary>
    private static IReadOnlyList<Scene> Scenes() =>
    [
        new("01-login", () => null),
        new("02-login-service-install", () => null, Tweak: m => m.PreviewServiceState(ServiceProbe(ScmState.NotInstalled, null))),
        new("03-login-service-repair", () => null, Tweak: m => m.PreviewServiceState(ServiceProbe(ScmState.Running, ServiceErrors.IntegrityFailed))),
        new("04-login-service-start", () => null, Tweak: m => m.PreviewServiceState(ServiceProbe(ScmState.Stopped, null))),
        new("05-login-minimum-size", () => null, Width: 820, Height: 560),

        new("10-connect-ready", () => Demo("ready", selected: NodeTokyo, configReady: true)),
        new("11-connect-no-line-selected", () => Demo("ready")),
        new("12-connect-connected", () => Demo("connected", selected: NodeTokyo, configReady: true, metering: true), Wide: true),
        new("13-connect-connecting", () => Demo("connecting", selected: NodeSingapore, configReady: true)),
        new("14-connect-admin-no-plan", () => Demo("blocked", authorized: false, role: "admin", reason: "paid_vip_required")),
        new("15-connect-expired", () => Demo("blocked", authorized: false, reason: "subscription_expired")),
        new("16-connect-quota-exhausted", () => Demo("blocked", authorized: false, reason: "quota_exhausted", remaining: 0)),
        new("17-connect-error", () => Demo("error", selected: NodeTokyo, configReady: true, error: ("tunnel_failed", "连接失败，请更换线路或稍后重试，或联系客服"))),
        new("18-connect-minimum-size", () => Demo("connected", selected: NodeLosAngeles, configReady: true, metering: true), Width: 820, Height: 560),

        new("20-lines", () => Demo("ready", selected: NodeHongKong, configReady: true), AppPage.Nodes, Wide: true),
        new("21-lines-empty-no-plan", () => Demo("blocked", authorized: false, nodes: false, reason: "paid_vip_required"), AppPage.Nodes),
        new("22-lines-empty-none", () => Demo("ready", nodes: false), AppPage.Nodes),
        new("23-lines-search-no-match", () => Demo("ready"), AppPage.Nodes, Tweak: m => m.Search = "不存在"),
        new("24-lines-connected", () => Demo("connected", selected: NodeTokyo, configReady: true, metering: true), AppPage.Nodes),
        new("25-lines-minimum-size", () => Demo("ready", selected: NodeLosAngeles, configReady: true), AppPage.Nodes, Width: 820, Height: 560),

        new("30-account", () => Demo("ready", selected: NodeTokyo, configReady: true), AppPage.Settings, Height: 1180, Wide: true),
        new("31-account-admin-no-plan", () => Demo("blocked", authorized: false, role: "admin", reason: "paid_vip_required"), AppPage.Settings, Height: 1180),
        new("32-account-service-problem", () => Demo("ready", selected: NodeTokyo, configReady: true), AppPage.Settings, Height: 1260,
            Tweak: m => m.PreviewServiceState(ServiceProbe(ScmState.Running, ServiceErrors.WorkerUnavailable))),
    ];

    public static async Task<int> RunAsync(string directory)
    {
        Directory.CreateDirectory(directory);
        var written = new List<object>();
        var texts = new SortedDictionary<string, List<string>>(StringComparer.Ordinal);
        var clipped = new List<object>();
        var environment = Shared.Env.ClientEnvironment.Parse(
            "APP_ENV=release\nAPI_BASE_URL=https://demo.example.test/api/v1/client\nWEBSITE_BASE_URL=https://site.example.test\n");

        async Task<(AppViewModel Model, SessionController Session)> Prepare(string theme, Func<CoreSnapshot?> snapshot, Action<AppViewModel>? tweak)
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
                Connected = true, Health = ServiceHealthState.Ready, Action = RecommendedAction.None, Message = "后台服务运行正常。",
            });
            await session.LaunchAsync();
            if (snapshot() is { } signedIn)
            {
                transport.Current = signedIn;
                await session.SignInAsync("demo@example.com", "preview-only", remember: false);
            }
            tweak?.Invoke(model);
            return (model, session);
        }

        async Task Render(string name, string theme, Scene scene, double scale)
        {
            var (model, _) = await Prepare(theme, scene.Snapshot, scene.Tweak);
            model.PreviewPage(scene.Page);
            var window = new MainWindow(model)
            {
                Width = scene.Width, Height = scene.Height, WindowStartupLocation = WindowStartupLocation.Manual, Left = 0, Top = 0, ShowInTaskbar = false,
            };
            window.Show();
            await Dispatcher.Yield(DispatcherPriority.ApplicationIdle);
            window.UpdateLayout();
            await Dispatcher.Yield(DispatcherPriority.ApplicationIdle);

            // Lay the window's content out again on its own at the full client size: a tall page (the account page) is
            // taller than the CI screen, and a shown window is clamped to the screen.
            var content = (FrameworkElement)window.Content;
            var client = new Size(content.ActualWidth, scene.Height - (window.ActualHeight - content.ActualHeight));
            window.Content = null;
            content.DataContext = model;
            content.SetValue(TextElement.FontFamilyProperty, window.FontFamily);
            content.SetValue(TextElement.FontSizeProperty, window.FontSize);
            content.SetValue(TextElement.ForegroundProperty, window.Foreground);
            TextOptions.SetTextFormattingMode(content, TextOptions.GetTextFormattingMode(window));
            content.Measure(client);
            content.Arrange(new Rect(client));
            content.UpdateLayout();
            await Dispatcher.Yield(DispatcherPriority.ApplicationIdle);
            var pixelsWide = (int)Math.Ceiling(content.ActualWidth * scale);
            var pixelsHigh = (int)Math.Ceiling(content.ActualHeight * scale);
            var bitmap = new RenderTargetBitmap(pixelsWide, pixelsHigh, 96 * scale, 96 * scale, PixelFormats.Pbgra32);
            // The window background first (the content only paints its own regions), then the content.
            var backdrop = new DrawingVisual();
            using (var context = backdrop.RenderOpen()) context.DrawRectangle(window.Background, null, new Rect(0, 0, content.ActualWidth, content.ActualHeight));
            bitmap.Render(backdrop);
            bitmap.Render(content);
            Save(bitmap, Path.Combine(directory, name + ".png"));
            written.Add(new { scenario = name, file = name + ".png", width = Math.Round(content.ActualWidth), height = Math.Round(content.ActualHeight), scale, theme });
            texts[name] = VisibleTexts(content, name, clipped);
            window.Close();
        }

        foreach (var scene in Scenes())
        {
            await Render(scene.Name, "light", scene, 1);
            await Render(scene.Name + "-dark", "dark", scene, 1);
            if (scene.Wide)
            {
                await Render(scene.Name + "-200pct", "light", scene, 2);
                await Render(scene.Name + "-dark-200pct", "dark", scene, 2);
            }
        }

        // The tray menu in its three situations, light and dark (WinForms, drawn by the same renderer as the real menu).
        foreach (var (name, signedIn, state) in new[] { ("40-tray-signed-out", false, ""), ("41-tray-ready", true, "ready"), ("42-tray-connected", true, "connected") })
        {
            foreach (var theme in new[] { "light", "dark" })
            {
                var scenario = theme == "light" ? name : name + "-dark";
                var snapshot = signedIn ? Demo(state, selected: NodeTokyo, configReady: true, metering: state == "connected") : null;
                var (model, _) = await Prepare(theme, () => snapshot, null);
                using var tray = new TrayMenu(() => { }, () => { }, () => { });
                var connected = model.State == "connected";
                tray.Update(model.TrayStatus, model.IsSignedIn, connected ? model.DisconnectCommand.CanExecute(null) : model.ConnectCommand.CanExecute(null), connected);
                texts[scenario] = [.. tray.Texts, model.TrayText];
                var file = RenderMenu(tray, Path.Combine(directory, scenario + ".png"));
                if (file is { } size) written.Add(new { scenario, file = scenario + ".png", width = size.Width, height = size.Height, scale = 1.0, theme });
            }
        }

        // The real window on the real desktop (system title bar, and Mica where Windows offers it), for the Windows 10/11 look.
        var desktop = new List<object>();
        foreach (var theme in new[] { "light", "dark" })
        {
            var name = theme == "light" ? "90-desktop-window" : "91-desktop-window-dark";
            try { desktop.Add(await CaptureDesktop(name, theme, directory, () => Prepare(theme, () => Demo("connected", selected: NodeTokyo, configReady: true, metering: true), null))); }
            catch (Exception error) { desktop.Add(new { scenario = name, error = error.GetType().Name + ": " + error.Message }); }
        }

        ThemeManager.Apply("light");
        var options = new JsonSerializerOptions { WriteIndented = true, Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping };
        File.WriteAllText(Path.Combine(directory, "texts.json"), JsonSerializer.Serialize(texts, options));
        File.WriteAllText(Path.Combine(directory, "index.json"), JsonSerializer.Serialize(new
        {
            count = written.Count,
            os = AppViewModel.OsVersion,
            mica_available = WindowBackdrop.MicaAvailable,
            scenarios = written,
            clipped,
            desktop,
        }, options));
        return written.Count > 0 ? 0 : 1;
    }

    private static void Save(BitmapSource bitmap, string path)
    {
        var encoder = new PngBitmapEncoder();
        encoder.Frames.Add(BitmapFrame.Create(bitmap));
        using var stream = File.Create(path);
        encoder.Save(stream);
    }

    /// <summary>
    /// Every string a user can see or hear on the window: visible text, text box contents and hints, accessible names,
    /// help texts and tool tips. A one-line text that does not fit its box (cut with "…" or simply clipped) is reported.
    /// </summary>
    private static List<string> VisibleTexts(FrameworkElement root, string scenario, List<object> clipped)
    {
        var found = new List<string>();
        var dpi = VisualTreeHelper.GetDpi(root).PixelsPerDip;
        void Add(string? text)
        {
            if (!string.IsNullOrWhiteSpace(text) && !found.Contains(text)) found.Add(text);
        }
        void Walk(DependencyObject node)
        {
            if (node is UIElement { Visibility: not Visibility.Visible }) return;
            if (node is FrameworkElement element)
            {
                Add(System.Windows.Automation.AutomationProperties.GetName(element));
                Add(System.Windows.Automation.AutomationProperties.GetHelpText(element));
                if (element.ToolTip is string tip) Add(tip);
            }
            switch (node)
            {
                case TextBlock block when block.ActualWidth > 0:
                    var text = block.Text.Length > 0 ? block.Text : string.Concat(block.Inlines.OfType<Run>().Select(r => r.Text));
                    Add(text);
                    if (block.TextWrapping == TextWrapping.NoWrap && text.Length > 0)
                    {
                        // Measured the way the block renders it (Display text mode in this app), so a fit is not reported as cut.
                        var measured = new FormattedText(text, System.Globalization.CultureInfo.CurrentUICulture, block.FlowDirection,
                            new Typeface(block.FontFamily, block.FontStyle, block.FontWeight, block.FontStretch), block.FontSize, Brushes.Black, null,
                            TextOptions.GetTextFormattingMode(block), dpi);
                        if (measured.WidthIncludingTrailingWhitespace > block.ActualWidth + 1)
                            clipped.Add(new { scenario, text, need = Math.Round(measured.WidthIncludingTrailingWhitespace), have = Math.Round(block.ActualWidth) });
                    }
                    break;
                case TextBox box:
                    Add(box.Text);
                    break;
            }
            for (var i = 0; i < VisualTreeHelper.GetChildrenCount(node); i++) Walk(VisualTreeHelper.GetChild(node, i));
        }
        Walk(root);
        return found;
    }

    /// <summary>Draw the WinForms tray menu to a PNG: shown for a moment (a drop-down only lays itself out when shown), drawn, closed.</summary>
    private static (int Width, int Height)? RenderMenu(TrayMenu tray, string path)
    {
        try
        {
            var strip = tray.Strip;
            strip.Show(new System.Drawing.Point(0, 0));
            strip.PerformLayout();
            using var bitmap = new System.Drawing.Bitmap(strip.Width, strip.Height);
            strip.DrawToBitmap(bitmap, new System.Drawing.Rectangle(0, 0, strip.Width, strip.Height));
            strip.Close();
            bitmap.Save(path, System.Drawing.Imaging.ImageFormat.Png);
            return (strip.Width, strip.Height);
        }
        catch (Exception error) when (error is InvalidOperationException or ArgumentException or System.ComponentModel.Win32Exception or System.Runtime.InteropServices.ExternalException)
        {
            File.WriteAllText(path + ".error.txt", error.ToString());
            return null;
        }
    }

    /// <summary>Show the real window with its system frame and backdrop, then copy that part of the screen.</summary>
    private static async Task<object> CaptureDesktop(string name, string theme, string directory, Func<Task<(AppViewModel Model, SessionController Session)>> prepare)
    {
        var (model, _) = await prepare();
        var window = new MainWindow(model) { Width = 1000, Height = 680, WindowStartupLocation = WindowStartupLocation.Manual, Left = 40, Top = 40, Topmost = true };
        window.SourceInitialized += (_, _) => WindowBackdrop.Apply(window, ThemeManager.IsDark);
        window.Show();
        window.Activate();
        await Task.Delay(900);
        await Dispatcher.Yield(DispatcherPriority.ApplicationIdle);
        var topLeft = window.PointToScreen(new Point(0, 0));
        var source = PresentationSource.FromVisual(window)!.CompositionTarget!.TransformToDevice;
        // The client area plus the system title bar above it.
        var caption = (int)Math.Round((SystemParameters.WindowCaptionHeight + SystemParameters.ResizeFrameHorizontalBorderHeight) * source.M22);
        var width = (int)Math.Round(window.ActualWidth * source.M11);
        var height = (int)Math.Round(window.ActualHeight * source.M22) + caption;
        using var bitmap = new System.Drawing.Bitmap(width, height);
        using (var graphics = System.Drawing.Graphics.FromImage(bitmap))
            graphics.CopyFromScreen((int)topLeft.X, (int)topLeft.Y - caption, 0, 0, new System.Drawing.Size(width, height));
        bitmap.Save(Path.Combine(directory, name + ".png"), System.Drawing.Imaging.ImageFormat.Png);
        var backdrop = WindowBackdrop.Describe(window);
        window.Close();
        return new { scenario = name, file = name + ".png", width, height, theme, backdrop };
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

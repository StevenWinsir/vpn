using System.ComponentModel;
using System.IO;
using System.Text.Json;
using System.Windows;
using System.Windows.Threading;
using Microsoft.Win32;

namespace AsterLink.App;

public partial class App : Application
{
    private const string SingleInstanceName = @"Local\AsterLink.App.Single";
    private const string ActivateEventName = @"Local\AsterLink.App.Activate";
    // How long a Windows sign-out or shutdown may wait for the usual disconnect-and-sync before AsterLink lets go.
    private static readonly TimeSpan SessionEndBudget = TimeSpan.FromSeconds(4);

    private Mutex? singleInstance;
    private EventWaitHandle? activate;
    private AppViewModel? model;
    private MainWindow? window;
    private TrayIcon? tray;
    private bool exiting;

    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);
        // The window can be hidden to the tray; only an explicit exit ends the process.
        ShutdownMode = ShutdownMode.OnExplicitShutdown;

        // CI / support mode: one real round trip through service and Core, result as JSON, no window.
        if (ServiceCheckOutputPath(e.Args) is { } checkPath)
        {
            _ = RunServiceCheckAsync(checkPath);
            return;
        }

        // Uninstaller helper: forget what the user chose to forget, for the account running it, with no window.
        if (Array.IndexOf(e.Args, "--cleanup-user-data") >= 0)
        {
            Shutdown(RunUserCleanup(e.Args));
            return;
        }

        // Design preview: render every page to PNG with a scripted Core (no service, no vault, no network).
        if (OptionValue(e.Args, "--preview") is { } previewDir)
        {
            _ = RunPreviewAsync(previewDir);
            return;
        }

        var selfTest = SelfTestOutputPath(e.Args);
        if (selfTest is null && !AcquireSingleInstance())
        {
            // A second launch would make the service end the first one's session; bring the first to the front instead.
            try { EventWaitHandle.OpenExisting(ActivateEventName).Set(); } catch (Exception error) when (error is WaitHandleCannotBeOpenedException or UnauthorizedAccessException) { }
            Shutdown(0);
            return;
        }

        model = new AppViewModel();
        model.ApplyTheme();
        window = new MainWindow(model);
        var shown = window;
        window.SourceInitialized += (_, _) => WindowBackdrop.Apply(shown, ThemeManager.IsDark);
        ThemeManager.Applied += OnThemeApplied;

        if (selfTest is not null)
        {
            // CI mode: prove the window really rendered, write a report, and exit. The report
            // says what was observed (actual size, rendered flag), not what was intended.
            window.ContentRendered += (_, _) => FinishSelfTest(window, selfTest);
            window.Closing += OnWindowClosing;
            window.Show();
            return;
        }

        tray = new TrayIcon(ShowWindow, ToggleConnection, () => _ = ExitAsync());
        model.StateChanged += UpdateTray;
        model.PropertyChanged += (_, _) => UpdateTray();
        model.ExitRequested += () => _ = ExitAsync();
        window.Closing += OnWindowClosing;
        window.Activated += (_, _) => _ = model.SyncAsync(force: false);
        window.Loaded += async (_, _) => await StartAsync();
        SessionEnding += OnSessionEnding;
        SystemEvents.PowerModeChanged += OnPowerModeChanged;
        SystemEvents.UserPreferenceChanged += OnUserPreferenceChanged;
        StartActivationListener();
        UpdateTray();
        window.Show();
    }

    private async Task StartAsync()
    {
        try { await model!.StartAsync(); }
        catch (Exception error) { model!.ReportFailure(error); }
    }

    // ---- single instance ---------------------------------------------------------------------

    private bool AcquireSingleInstance()
    {
        singleInstance = new Mutex(initiallyOwned: true, SingleInstanceName, out var created);
        if (!created) { singleInstance.Dispose(); singleInstance = null; return false; }
        activate = new EventWaitHandle(false, EventResetMode.AutoReset, ActivateEventName);
        return true;
    }

    private void StartActivationListener()
    {
        if (activate is null) return;
        var listener = new Thread(() =>
        {
            while (activate.WaitOne()) Dispatcher.BeginInvoke(ShowWindow);
        }) { IsBackground = true, Name = "AsterLink.Activation" };
        listener.Start();
    }

    // ---- window and tray ---------------------------------------------------------------------

    private void ShowWindow()
    {
        if (window is null) return;
        window.Show();
        if (window.WindowState == WindowState.Minimized) window.WindowState = WindowState.Normal;
        window.Activate();
    }

    /// <summary>
    /// "×", Alt+F4 and the taskbar's "关闭窗口" only hide the window, in every state (signed out, idle, connected, failed):
    /// the process, the connection and the sync go on, and the tray icon stays (handoff 6.4, D4). Quitting is the tray's
    /// "退出" or the account page's "退出 AsterLink". During a Windows sign-out or shutdown the window is let go.
    /// </summary>
    private void OnWindowClosing(object? sender, CancelEventArgs e)
    {
        if (exiting) return;
        e.Cancel = true;
        window!.Hide();
        // No tray in the self-test: nothing to point at, and the user's "already told" flag is left alone.
        if (tray is not null && !model!.TrayNoticeShown)
        {
            model.TrayNoticeShown = true;
            // Windows 11 puts a new tray icon in the overflow area, so say where to look (D6: once, never a dialog).
            tray?.Notify("AsterLink 已在后台运行", "点击任务栏右下角的 AsterLink 图标即可重新打开；如果没有看到，请点击“^”显示隐藏的图标。");
        }
    }

    /// <summary>
    /// Windows is signing out or shutting down. From here the window may close (WPF ends the application right after this
    /// handler), and the usual disconnect-and-sync of "退出" runs first, pumped on this thread for at most
    /// <see cref="SessionEndBudget"/> so AsterLink never shows up among the apps that block shutdown. If it cannot finish,
    /// the service still stops the connection when this process goes away.
    /// </summary>
    private void OnSessionEnding(object? sender, SessionEndingCancelEventArgs e)
    {
        if (exiting) return;
        exiting = true;
        var frame = new DispatcherFrame();
        async Task Shutdown()
        {
            try { await model!.ShutdownAsync(); }
            catch (Exception) { /* the service closes the connection when the pipe goes away */ }
        }
        _ = Task.WhenAny(Shutdown(), Task.Delay(SessionEndBudget)).ContinueWith(_ => frame.Continue = false, TaskScheduler.Default);
        try { Dispatcher.PushFrame(frame); }
        catch (InvalidOperationException) { /* message processing is suspended here: leave it to the service */ }
        SystemEvents.PowerModeChanged -= OnPowerModeChanged;
        SystemEvents.UserPreferenceChanged -= OnUserPreferenceChanged;
        tray?.Dispose();
        tray = null;
    }

    private void OnThemeApplied(bool dark)
    {
        if (window is not null) WindowBackdrop.Apply(window, dark);
        tray?.SetTheme(dark);
    }

    // "跟随系统": Windows switched between light and dark apps.
    private void OnUserPreferenceChanged(object? sender, UserPreferenceChangedEventArgs e)
    {
        if (e.Category == UserPreferenceCategory.General) Dispatcher.BeginInvoke(() => model?.FollowSystemTheme());
    }

    private void ToggleConnection()
    {
        if (model is null) return;
        var command = model.State == "connected" ? model.DisconnectCommand : model.ConnectCommand;
        if (command.CanExecute(null)) command.Execute(null);
    }

    private void UpdateTray()
    {
        if (model is null || tray is null) return;
        var connected = model.State == "connected";
        tray.Update(model.TrayText, model.TrayStatus, model.State, model.IsSignedIn,
            connected ? model.DisconnectCommand.CanExecute(null) : model.ConnectCommand.CanExecute(null), connected);
    }

    private void OnPowerModeChanged(object? sender, PowerModeChangedEventArgs e)
    {
        // After sleep nothing cached is trusted: re-sync the catalog and authorization at once.
        if (e.Mode == PowerModes.Resume) Dispatcher.BeginInvoke(() => _ = model?.SyncAsync(force: true));
    }

    /// <summary>Disconnect, sync the last usage (bounded), then leave. The saved password stays unless the user signed out.</summary>
    private async Task ExitAsync()
    {
        if (exiting) return;
        exiting = true;
        if (window is not null && model is { IsLive: true }) ShowWindow(); // show why it takes a moment
        try { await model!.ShutdownAsync(); }
        catch (Exception) { /* the service closes the Core when the pipe goes away */ }
        SystemEvents.PowerModeChanged -= OnPowerModeChanged;
        SystemEvents.UserPreferenceChanged -= OnUserPreferenceChanged;
        tray?.Dispose();
        singleInstance?.ReleaseMutex();
        Shutdown(0);
    }

    // ---- CI / support modes ------------------------------------------------------------------

    private async Task RunPreviewAsync(string directory)
    {
        var code = 1;
        try { code = await PreviewRunner.RunAsync(Path.GetFullPath(directory)); }
        catch (Exception error)
        {
            Directory.CreateDirectory(directory);
            File.WriteAllText(Path.Combine(directory, "preview-error.txt"), error.ToString());
        }
        Shutdown(code);
    }

    /// <summary><c>--cleanup-user-data [--passwords] [--preferences] [--report file]</c>; exit 0 only if everything asked for was done.</summary>
    private static int RunUserCleanup(string[] args)
    {
        var result = AsterLink.Shared.Client.UserDataCleanup.Run(
            passwords: Array.IndexOf(args, "--passwords") >= 0,
            preferences: Array.IndexOf(args, "--preferences") >= 0,
            deletePasswords: () => new AsterLink.Platform.CredentialManagerStore().DeleteAllProductItems(),
            preferencesDirectory: AsterLink.Shared.Client.UserDataCleanup.DefaultPreferencesDirectory());
        if (OptionValue(args, "--report") is { } report)
        {
            try
            {
                File.WriteAllText(report, JsonSerializer.Serialize(new
                {
                    passwords_removed = result.PasswordsRemoved,
                    preferences_removed = result.PreferencesRemoved,
                    error = result.Error,
                }));
            }
            catch (Exception error) when (error is IOException or UnauthorizedAccessException) { /* the exit code still tells */ }
        }
        return result.Error is null ? 0 : 1;
    }

    private static string? OptionValue(string[] args, string name)
    {
        var index = Array.IndexOf(args, name);
        return index >= 0 && index + 1 < args.Length ? args[index + 1] : null;
    }

    private static string? ServiceCheckOutputPath(string[] args)
    {
        var index = Array.IndexOf(args, "--service-check");
        return index >= 0 && index + 1 < args.Length ? args[index + 1] : null;
    }

    private async Task RunServiceCheckAsync(string path)
    {
        var code = 1;
        try
        {
            var layout = AsterLink.Shared.Service.InstallLayout.FromComponentDirectory(AppContext.BaseDirectory);
            var holdIndex = Array.IndexOf(Environment.GetCommandLineArgs(), "--service-hold");
            var hold = holdIndex >= 0 && holdIndex + 1 < Environment.GetCommandLineArgs().Length
                && int.TryParse(Environment.GetCommandLineArgs()[holdIndex + 1], out var seconds) ? TimeSpan.FromSeconds(Math.Clamp(seconds, 0, 600)) : (TimeSpan?)null;
            var result = await AsterLink.Platform.ServiceDiagnostics.ProbeAsync(layout, hold: hold);
            File.WriteAllText(path, JsonSerializer.Serialize(result, new JsonSerializerOptions
            {
                PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
                WriteIndented = true,
                Converters = { new System.Text.Json.Serialization.JsonStringEnumConverter(JsonNamingPolicy.SnakeCaseLower) },
            }));
            code = result.Connected ? 0 : 1;
        }
        catch (Exception error)
        {
            File.WriteAllText(path, JsonSerializer.Serialize(new { connected = false, error_code = "check_failed", error_message = error.Message }));
        }
        Shutdown(code);
    }

    [System.Runtime.InteropServices.DllImport("user32.dll")]
    private static extern IntPtr SendMessage(IntPtr hwnd, int message, IntPtr wParam, IntPtr lParam);

    private static string? SelfTestOutputPath(string[] args)
    {
        var index = Array.IndexOf(args, "--self-test");
        return index >= 0 && index + 1 < args.Length ? args[index + 1] : null;
    }

    private void FinishSelfTest(MainWindow window, string path)
    {
        // Every page must at least load and lay out with the real view model: a XAML or binding-resource mistake fails here.
        var pages = new Dictionary<string, bool>();
        foreach (var (name, create) in new (string, Func<System.Windows.Controls.UserControl>)[]
        {
            ("login", () => new Views.LoginView()), ("connect", () => new Views.ConnectView()),
            ("lines", () => new Views.LinesView()), ("account", () => new Views.AccountView()),
        })
        {
            try
            {
                var view = create();
                view.DataContext = model;
                view.Measure(new Size(760, 2000));
                view.Arrange(new Rect(0, 0, 760, view.DesiredSize.Height));
                view.UpdateLayout();
                pages[name] = view.DesiredSize.Height > 0;
            }
            catch (Exception) { pages[name] = false; }
        }
        var rendered = window.IsLoaded && window.IsVisible;
        var width = window.ActualWidth;
        var height = window.ActualHeight;

        // "×", Alt+F4 and the taskbar's "关闭窗口" all arrive as WM_CLOSE: the window must hide, not close (handoff 6.4).
        var closed = false;
        window.Closed += (_, _) => closed = true;
        SendMessage(new System.Windows.Interop.WindowInteropHelper(window).Handle, 0x0010 /* WM_CLOSE */, IntPtr.Zero, IntPtr.Zero);
        var hidesOnClose = !closed && !window.IsVisible;

        var passed = model!.CheckResults.All(c => c.Passed) && pages.Values.All(ok => ok) && hidesOnClose;
        var report = new
        {
            window = new { rendered, width, height, backdrop = WindowBackdrop.Describe(window), hides_on_close = hidesOnClose },
            platform = model.Report,
            checks = model.CheckResults,
            pages,
            passed,
        };
        File.WriteAllText(path, JsonSerializer.Serialize(report, new JsonSerializerOptions
        {
            PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
            WriteIndented = true,
        }));
        exiting = true;
        Shutdown(passed && rendered ? 0 : 1);
    }
}

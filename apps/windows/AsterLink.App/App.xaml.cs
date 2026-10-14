using System.ComponentModel;
using System.IO;
using System.Text.Json;
using System.Windows;
using System.Windows.Interop;
using System.Windows.Media.Imaging;
using Microsoft.Win32;

namespace AsterLink.App;

public partial class App : Application
{
    private const string SingleInstanceName = @"Local\AsterLink.App.Single";
    private const string ActivateEventName = @"Local\AsterLink.App.Activate";

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

        if (selfTest is not null)
        {
            // CI mode: prove the window really rendered, write a report, and exit. The report
            // says what was observed (actual size, rendered flag), not what was intended.
            window.ContentRendered += (_, _) => FinishSelfTest(window, selfTest);
            window.Show();
            return;
        }

        window.Icon = ToImageSource(TrayIcon.WindowIcon());
        tray = new TrayIcon(ShowWindow, ToggleConnection, () => _ = ExitAsync());
        model.StateChanged += UpdateTray;
        model.ExitRequested += () => _ = ExitAsync();
        window.Closing += OnWindowClosing;
        window.Activated += (_, _) => _ = model.SyncAsync(force: false);
        window.Loaded += async (_, _) => await StartAsync();
        SystemEvents.PowerModeChanged += OnPowerModeChanged;
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

    private void OnWindowClosing(object? sender, CancelEventArgs e)
    {
        if (exiting) return;
        e.Cancel = true;
        if (model!.IsLive)
        {
            window!.Hide();
            if (!model.TrayNoticeShown)
            {
                model.TrayNoticeShown = true;
                tray?.Notify("AsterLink 仍在运行", "连接保持中。点击通知区域的图标可重新打开；选择“退出”才会停止代理。");
            }
        }
        else
        {
            _ = ExitAsync();
        }
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
        tray.Update(model.TrayText, model.State, model.IsSignedIn,
            connected ? model.DisconnectCommand.CanExecute(null) : model.ConnectCommand.CanExecute(null), connected);
    }

    private void OnPowerModeChanged(object? sender, PowerModeChangedEventArgs e)
    {
        // After sleep nothing cached is trusted: re-sync the catalog and authorization at once.
        if (e.Mode == PowerModes.Resume) Dispatcher.BeginInvoke(() => _ = model?.SyncAsync(force: true));
    }

    /// <summary>Stop the proxy, settle the last traffic batch (bounded), then leave. The saved password stays unless the user signed out.</summary>
    private async Task ExitAsync()
    {
        if (exiting) return;
        exiting = true;
        if (window is not null && model is { IsLive: true }) ShowWindow(); // show why it takes a moment
        try { await model!.ShutdownAsync(); }
        catch (Exception) { /* the service closes the Core when the pipe goes away */ }
        SystemEvents.PowerModeChanged -= OnPowerModeChanged;
        tray?.Dispose();
        singleInstance?.ReleaseMutex();
        Shutdown(0);
    }

    private static BitmapSource ToImageSource(System.Drawing.Icon icon) =>
        Imaging.CreateBitmapSourceFromHIcon(icon.Handle, Int32Rect.Empty, BitmapSizeOptions.FromEmptyOptions());

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
            ("nodes", () => new Views.NodesView()), ("settings", () => new Views.SettingsView()),
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
        var passed = model!.CheckResults.All(c => c.Passed) && pages.Values.All(ok => ok);
        var report = new
        {
            window = new { rendered = window.IsLoaded && window.IsVisible, width = window.ActualWidth, height = window.ActualHeight },
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
        Shutdown(passed && window.IsVisible ? 0 : 1);
    }
}

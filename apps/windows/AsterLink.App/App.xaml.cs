using System.IO;
using System.Text.Json;
using System.Windows;

namespace AsterLink.App;

public partial class App : Application
{
    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);

        // CI / support mode: one real round trip through service and Core, result as JSON, no window.
        if (ServiceCheckOutputPath(e.Args) is { } checkPath)
        {
            _ = RunServiceCheckAsync(checkPath);
            return;
        }

        var window = new MainWindow();
        var selfTest = SelfTestOutputPath(e.Args);
        if (selfTest is not null)
        {
            // CI mode: prove the window really rendered, write a report, and exit. The report
            // says what was observed (actual size, rendered flag), not what was intended.
            window.ContentRendered += (_, _) => FinishSelfTest(window, selfTest);
        }
        window.Show();
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
        var model = (MainViewModel)window.DataContext;
        var passed = model.CheckResults.All(c => c.Passed);
        var report = new
        {
            window = new { rendered = window.IsLoaded && window.IsVisible, width = window.ActualWidth, height = window.ActualHeight },
            platform = model.Report,
            checks = model.CheckResults,
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

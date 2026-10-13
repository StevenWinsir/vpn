using System.IO;
using System.Text.Json;
using System.Windows;

namespace AsterLink.App;

public partial class App : Application
{
    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);

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

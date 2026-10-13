using System.Windows.Media;
using AsterLink.Shared.Platform;

namespace AsterLink.App;

public sealed record FactRow(string Label, string Value);

public sealed record CheckRow(string Name, bool Passed)
{
    public string Mark => Passed ? "通过" : "失败";
    public Brush Brush => (Brush)System.Windows.Application.Current.Resources[Passed ? "PassBrush" : "FailBrush"];
}

/// <summary>Round 3 view model: environment facts plus the privileged-service card. The real GUI will render Core snapshots (round 4).</summary>
public sealed class MainViewModel
{
    public ServicePanel Service { get; } = new();

    public PlatformReport Report { get; } = PlatformReport.Capture("AsterLink.App");
    public IReadOnlyList<SelfCheckResult> CheckResults { get; } = FoundationSelfCheck.Run();

    public IReadOnlyList<FactRow> Facts =>
    [
        new("操作系统", Report.OsDescription),
        new("系统架构", Report.OsArchitecture),
        new("进程架构", Report.ProcessArchitecture),
        new("运行时", Report.RuntimeDescription),
        new("原生 Windows x64", Report.IsNativeWindowsX64 ? "是" : "否"),
    ];

    public IReadOnlyList<CheckRow> Checks => [.. CheckResults.Select(c => new CheckRow(c.Name, c.Passed))];
}

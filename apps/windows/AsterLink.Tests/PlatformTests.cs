using System.Runtime.InteropServices;
using System.Text.Json;
using AsterLink.Shared.Platform;

namespace AsterLink.Tests;

public class PlatformTests
{
    [Fact]
    public void Report_states_the_real_os_and_architecture()
    {
        var report = PlatformReport.Capture("tests");
        Assert.Equal(RuntimeInformation.OSArchitecture.ToString(), report.OsArchitecture);
        Assert.Equal(RuntimeInformation.ProcessArchitecture.ToString(), report.ProcessArchitecture);
        Assert.Equal(OperatingSystem.IsWindows(), report.IsWindows);
        Assert.StartsWith(".NET 10.", report.RuntimeDescription);
    }

    [Fact]
    public void Only_a_genuine_x64_windows_host_counts_as_native_windows_x64()
    {
        var expected = OperatingSystem.IsWindows()
            && RuntimeInformation.OSArchitecture == Architecture.X64
            && RuntimeInformation.ProcessArchitecture == Architecture.X64;
        Assert.Equal(expected, PlatformReport.Capture("tests").IsNativeWindowsX64);
    }

    [Fact]
    public void Report_json_uses_snake_case_keys()
    {
        using var document = JsonDocument.Parse(PlatformReport.Capture("tests").ToJson());
        Assert.True(document.RootElement.TryGetProperty("os_architecture", out _));
        Assert.True(document.RootElement.TryGetProperty("is_native_windows_x64", out _));
    }

    [Fact]
    public void Foundation_self_check_passes_everywhere()
    {
        var results = FoundationSelfCheck.Run();
        Assert.NotEmpty(results);
        Assert.All(results, r => Assert.True(r.Passed, r.Name + ": " + r.Detail));
    }
}

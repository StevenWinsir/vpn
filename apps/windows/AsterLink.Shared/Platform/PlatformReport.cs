using System.Runtime.InteropServices;
using System.Text.Json;

namespace AsterLink.Shared.Platform;

/// <summary>
/// Facts about the machine and runtime a binary actually ran on. Round 1 acceptance requires
/// "verify the real OS and CPU architecture", so every report states them instead of assuming.
/// </summary>
public sealed class PlatformReport
{
    public string Component { get; init; } = "";
    public string AppVersion { get; init; } = "";
    public string OsDescription { get; init; } = "";
    public string OsVersion { get; init; } = "";
    public string OsArchitecture { get; init; } = "";
    public string ProcessArchitecture { get; init; } = "";
    public string RuntimeDescription { get; init; } = "";
    public string RuntimeIdentifier { get; init; } = "";
    public bool IsWindows { get; init; }
    public bool Is64BitProcess { get; init; }
    public int ProcessorCount { get; init; }

    /// <summary>True only for a genuine x64 Windows host running an x64 process (not emulated ARM64).</summary>
    public bool IsNativeWindowsX64 { get; init; }

    public static PlatformReport Capture(string component)
    {
        var os = RuntimeInformation.OSArchitecture;
        var process = RuntimeInformation.ProcessArchitecture;
        var windows = OperatingSystem.IsWindows();
        return new PlatformReport
        {
            Component = component,
            AppVersion = typeof(PlatformReport).Assembly.GetName().Version?.ToString(3) ?? "0.0.0",
            OsDescription = RuntimeInformation.OSDescription,
            OsVersion = Environment.OSVersion.Version.ToString(),
            OsArchitecture = os.ToString(),
            ProcessArchitecture = process.ToString(),
            RuntimeDescription = RuntimeInformation.FrameworkDescription,
            RuntimeIdentifier = RuntimeInformation.RuntimeIdentifier,
            IsWindows = windows,
            Is64BitProcess = Environment.Is64BitProcess,
            ProcessorCount = Environment.ProcessorCount,
            IsNativeWindowsX64 = windows && os == Architecture.X64 && process == Architecture.X64,
        };
    }

    public string ToJson() => JsonSerializer.Serialize(this, JsonOptions);

    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        WriteIndented = true,
    };
}

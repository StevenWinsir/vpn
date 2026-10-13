using System.Diagnostics;
using System.Security.Principal;
using System.Text;
using AsterLink.Platform;
using AsterLink.Shared.Service;

namespace AsterLink.Tests.Windows;

/// <summary>Runs only on an elevated token; otherwise it is reported as skipped, never as passed.</summary>
public sealed class AdminFactAttribute : FactAttribute
{
    public AdminFactAttribute()
    {
        if (!OperatingSystem.IsWindows()) Skip = "needs Windows";
        else if (!ProtectedFiles.IsElevated()) Skip = "needs an elevated token";
    }
}

public sealed class WindowsFactAttribute : FactAttribute
{
    public WindowsFactAttribute()
    {
        if (!OperatingSystem.IsWindows()) Skip = "needs Windows";
    }
}

/// <summary>Needs the real Go Core: ASTERLINK_TEST_CORE names the asterlink-core.exe the CI build produced.</summary>
public sealed class CoreFactAttribute : FactAttribute
{
    public CoreFactAttribute()
    {
        if (!OperatingSystem.IsWindows()) Skip = "needs Windows";
        else if (CoreSupport.Path is null) Skip = "ASTERLINK_TEST_CORE is not set";
    }
}

internal static class CoreSupport
{
    public static string? Path => Environment.GetEnvironmentVariable("ASTERLINK_TEST_CORE") is { Length: > 0 } p && File.Exists(p) ? System.IO.Path.GetFullPath(p) : null;
}

/// <summary>A disposable scratch directory below the machine temp folder.</summary>
internal sealed class Scratch : IDisposable
{
    public string Root { get; } = Directory.CreateTempSubdirectory("asterlink-win-").FullName;

    public string Write(string relative, string content)
    {
        var path = System.IO.Path.Combine(Root, relative.Replace('/', '\\'));
        Directory.CreateDirectory(System.IO.Path.GetDirectoryName(path)!);
        File.WriteAllText(path, content);
        return path;
    }

    public void Dispose()
    {
        try { Directory.Delete(Root, recursive: true); } catch (IOException) { } catch (UnauthorizedAccessException) { }
    }
}

internal static class Stage
{
    /// <summary>Writes a small but complete install tree plus its manifest, and returns the layout.</summary>
    public static InstallLayout Build(string root, string coreBytes = "core")
    {
        string Put(string relative, string text)
        {
            var path = System.IO.Path.Combine(root, relative.Replace('/', '\\'));
            Directory.CreateDirectory(System.IO.Path.GetDirectoryName(path)!);
            File.WriteAllText(path, text);
            return path;
        }
        var contents = new (string Path, string Text, string? Role)[]
        {
            ("app/AsterLink.exe", "gui", "gui"),
            ("app/AsterLink.dll", "guicode", null),
            ("service/AsterLink.Service.exe", "svc", "service"),
            ("core/asterlink-core.exe", coreBytes, "core"),
            ("build-config.json", """{"APP_ENV":"development","API_BASE_URL":"https://demo.hyshentou.cn/api/v1/client","WEBSITE_BASE_URL":"https://test.hyshentou.cn"}""", "config"),
        };
        var files = contents.Select(c =>
        {
            var full = Put(c.Path, c.Text);
            return new ManifestFile { Path = c.Path, Sha256 = ReleaseManifest.HashFile(full), Size = new FileInfo(full).Length, Role = c.Role };
        }).ToList();
        var manifest = new ReleaseManifest { FormatVersion = 1, Version = BuildVersion.Current, Channel = "internal", ProtocolVersion = 1, Files = files };
        File.WriteAllText(System.IO.Path.Combine(root, ServiceConstants.ManifestFile), manifest.ToJson(), new UTF8Encoding(false));
        return new InstallLayout(root);
    }
}

internal static class ChildProcess
{
    /// <summary>A harmless process that stays alive for a minute: ping to localhost, 60 echoes.</summary>
    public static Process StartSleeper()
    {
        var ping = System.IO.Path.Combine(Environment.SystemDirectory, "ping.exe");
        return Process.Start(new ProcessStartInfo(ping, "-n 60 127.0.0.1") { UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true })!;
    }
}

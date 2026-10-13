namespace AsterLink.Shared.Service;

/// <summary>What the GUI could learn about the process serving the pipe.</summary>
public sealed record ServerFacts(
    int PipeServerProcessId,
    ScmState ScmState,
    int ScmProcessId,
    string? ScmBinaryPath,
    string? ScmStartName,
    bool ProcessInspectable,
    string? ImagePath,
    string? UserSid);

/// <summary>
/// Whether the other end of the pipe is the genuine service. The binding that matters is the Service
/// Control Manager's: the pipe server's PID must be the PID the SCM reports for the registered service,
/// whose binary path and account only an administrator can change. A standard user usually cannot open
/// a SYSTEM service process at all, so inspecting the process is an extra check applied when the OS
/// allows it, never a requirement (round 3 finding on a real Windows 10 machine: demanding it locked
/// every ordinary user out).
/// </summary>
public static class ServerPolicy
{
    public const string SystemSid = "S-1-5-18";

    /// <summary>True when the registered binary is exactly the quoted protected service path, with no arguments.</summary>
    public static bool BinaryPathIs(string? registeredPath, string expectedExe)
    {
        if (registeredPath is null) return false;
        var path = registeredPath.Trim();
        if (path.Length < 2 || path[0] != '"') return false;
        var close = path.IndexOf('"', 1);
        return close == path.Length - 1 && WinPath.Equal(path[1..close], expectedExe);
    }

    /// <summary>Null when accepted, otherwise the reason it was rejected.</summary>
    public static string? Evaluate(ServerFacts facts, string expectedServiceExe)
    {
        if (facts.PipeServerProcessId <= 0) return "cannot identify the pipe server";
        if (facts.ScmState != ScmState.Running || facts.ScmProcessId != facts.PipeServerProcessId)
            return "the pipe is not served by the registered AsterLink service process";
        if (!BinaryPathIs(facts.ScmBinaryPath, expectedServiceExe))
            return "the registered service does not point at the protected install path";
        if (!string.Equals(facts.ScmStartName, "LocalSystem", StringComparison.OrdinalIgnoreCase))
            return "the registered service does not run as LocalSystem";

        if (facts.ProcessInspectable)
        {
            if (!WinPath.Equal(facts.ImagePath, expectedServiceExe)) return "the pipe server is not running from the protected install path";
            if (!string.Equals(facts.UserSid, SystemSid, StringComparison.Ordinal)) return "the pipe server is not running as SYSTEM";
        }
        return null;
    }
}

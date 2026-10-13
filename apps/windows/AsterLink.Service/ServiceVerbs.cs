using System.Diagnostics;
using System.Text.Json;
using AsterLink.Platform;
using AsterLink.Shared.Service;

namespace AsterLink.Service;

/// <summary>
/// The development installer: command-line verbs of the service executable. They exist so the
/// privileged part of installation is one small, reviewable program that the GUI can raise through
/// UAC (<c>repair</c>) or that a stage tree can run (<c>install</c>). It is the INTERNAL channel: nothing is
/// code-signed, and the trust placed in the files comes from the protected directory they are copied
/// into, not from a signature. The formal installer and Authenticode arrive in round 8.
/// </summary>
public static class ServiceVerbs
{
    private static readonly JsonSerializerOptions Json = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        WriteIndented = true,
        DefaultIgnoreCondition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull,
    };

    public static bool IsVerb(string[] args) => args.Length > 0 && args[0] is "install" or "uninstall" or "repair" or "status";

    public static int Run(string[] args)
    {
        try
        {
            return args[0] switch
            {
                "install" => Install(args),
                "uninstall" => Uninstall(),
                "repair" => Repair(),
                "status" => Status(),
                _ => Usage(),
            };
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or InvalidOperationException
                                          or System.ComponentModel.Win32Exception or ManifestException)
        {
            return Fail(ServiceConstants.ExitCodes.Failed, "failed", error.Message);
        }
    }

    private static int Usage()
    {
        Console.Error.WriteLine("usage: AsterLink.Service.exe install --stage <dir> | uninstall | repair | status");
        return ServiceConstants.ExitCodes.Usage;
    }

    private static int Emit(object result)
    {
        Console.WriteLine(JsonSerializer.Serialize(result, Json));
        return ServiceConstants.ExitCodes.Ok;
    }

    private static int Fail(int exitCode, string code, string message)
    {
        Console.WriteLine(JsonSerializer.Serialize(new { ok = false, code, message }, Json));
        return exitCode;
    }

    private static int NeedElevation() =>
        Fail(ServiceConstants.ExitCodes.NeedsElevation, "needs_elevation", "this action needs an administrator token");

    // ---- install ---------------------------------------------------------------------------------

    private static int Install(string[] args)
    {
        var stageIndex = Array.IndexOf(args, "--stage");
        if (args.Length != 3 || stageIndex != 1) return Usage();
        if (!ProtectedFiles.IsElevated()) return NeedElevation();

        var stageRoot = Path.GetFullPath(args[2]).TrimEnd('\\');
        if (!WinPath.IsSafeAbsolute(stageRoot)) return Fail(ServiceConstants.ExitCodes.Usage, "bad_stage", "the stage path is not a plain absolute path");
        var stage = new InstallLayout(stageRoot);
        var target = InstallVerifier.ProgramFilesLayout();
        if (WinPath.Equal(stage.Root, target.Root) || WinPath.IsUnder(target.Root, stage.Root))
            return Fail(ServiceConstants.ExitCodes.Usage, "bad_stage", "the stage must not be the install location");

        // The stage is checked as a whole before anything on this machine is touched.
        var manifest = ReleaseManifest.Parse(File.ReadAllBytes(stage.Manifest));
        if (manifest.Channel != ReleaseManifest.InternalChannel)
            return Fail(ServiceConstants.ExitCodes.IntegrityFailed, "wrong_channel", "this installer only installs the internal channel");
        if (manifest.Version != BuildVersion.Current)
            return Fail(ServiceConstants.ExitCodes.IntegrityFailed, "version_mismatch", $"the stage is {manifest.Version} but this installer is {BuildVersion.Current}");
        var stageProblems = ManifestVerifier.Verify(stage.Root, manifest);
        if (stageProblems.Count > 0)
            return Fail(ServiceConstants.ExitCodes.IntegrityFailed, "stage_integrity", Summarise(stageProblems.Select(p => $"{p.Kind}: {p.Path}")));
        _ = AsterLink.Shared.Service.BuildConfig.Parse(File.ReadAllBytes(stage.BuildConfig));

        if (InstallLayout.FromComponentDirectory(AppContext.BaseDirectory) is { } own && own.IsAt(target.Root))
            return Fail(ServiceConstants.ExitCodes.Usage, "bad_origin", "run the installer from the stage, not from the installed copy");

        var registered = false;
        try
        {
            RemoveService();
            RemoveTree(target);

            Directory.CreateDirectory(target.Root);
            ProtectedFiles.Protect(target.Root, usersMayRead: true);
            PrepareProgramData();

            foreach (var name in InstallLayout.ManagedDirectories) CopyTree(stage.Join(name), target.Join(name));
            File.Copy(stage.BuildConfig, target.BuildConfig);
            File.Copy(stage.Manifest, target.Manifest);

            var report = InstallVerifier.Verify(target);
            if (!report.Trusted)
                throw new InvalidOperationException("the installed copy failed verification: " + Summarise(report.Problems));

            CreateService(target);
            registered = true;
            StartAndWait();
        }
        catch
        {
            // An install that did not finish must not leave a runnable service or a half-written tree.
            if (registered || ScmClient.Query().State != ScmState.NotInstalled) RemoveService();
            RemoveTree(target);
            throw;
        }
        return Emit(new { ok = true, action = "install", version = manifest.Version, channel = manifest.Channel, service = ScmClient.Query().State.ToString() });
    }

    // ---- uninstall -------------------------------------------------------------------------------

    private static int Uninstall()
    {
        if (!ProtectedFiles.IsElevated()) return NeedElevation();
        var target = InstallVerifier.ProgramFilesLayout();
        if (InstallLayout.FromComponentDirectory(AppContext.BaseDirectory) is { } own && own.IsAt(target.Root))
            return Fail(ServiceConstants.ExitCodes.Usage, "bad_origin", "a program cannot delete its own files: run uninstall from a copy outside the install directory");

        RemoveService();
        RemoveTree(target);
        var data = RecoveryStore.DefaultDirectory;
        if (Directory.Exists(data) && !IsLink(data)) Directory.Delete(data, recursive: true);
        return Emit(new { ok = true, action = "uninstall", service = ScmClient.Query().State.ToString(), root_exists = Directory.Exists(target.Root) });
    }

    // ---- repair ----------------------------------------------------------------------------------

    private static int Repair()
    {
        if (!ProtectedFiles.IsElevated()) return NeedElevation();
        var target = InstallVerifier.ProgramFilesLayout();
        if (!Directory.Exists(target.Root))
            return Fail(ServiceConstants.ExitCodes.IntegrityFailed, "not_installed", "nothing is installed; run install from a stage");
        if (InstallLayout.FromComponentDirectory(AppContext.BaseDirectory) is not { } own || !own.IsAt(target.Root))
            return Fail(ServiceConstants.ExitCodes.Usage, "bad_origin", "repair runs from the installed copy");

        // Repair re-establishes protection and registration; it never replaces content. A file that
        // no longer matches the manifest cannot be trusted by a repair and needs a fresh install.
        ProtectedFiles.Protect(target.Root, usersMayRead: true);
        ProtectedFiles.ResetInheritedAcls(target.Root);
        PrepareProgramData();
        var store = RecoveryStore.Default();
        var archived = store.ArchiveCorrupt() is not null && store.Read() is null;

        var report = InstallVerifier.Verify(target);
        if (!report.Trusted)
            return Fail(ServiceConstants.ExitCodes.IntegrityFailed, "integrity_failed", Summarise(report.Problems));

        var scm = ScmClient.Query();
        if (scm.State == ScmState.NotInstalled) CreateService(target);
        else if (!ScmClient.BinaryPathIs(scm, target.ServiceExe) || !string.Equals(scm.StartName, "LocalSystem", StringComparison.OrdinalIgnoreCase))
            ConfigureService(target);
        // A running service keeps the "recovery blocked" decision it made at start; a restart re-runs recovery.
        if (archived && ScmClient.Query().State != ScmState.Stopped) StopService();
        if (ScmClient.Query().State != ScmState.Running) StartAndWait();
        return Emit(new { ok = true, action = "repair", archived_record = archived, service = ScmClient.Query().State.ToString() });
    }

    // ---- status ----------------------------------------------------------------------------------

    private static int Status()
    {
        var target = InstallVerifier.ProgramFilesLayout();
        var scm = ScmClient.Query();
        var store = RecoveryStore.Default();
        IReadOnlyList<string> problems = Directory.Exists(target.Root) ? InstallVerifier.Verify(target).Problems : ["install root does not exist"];
        return Emit(new
        {
            installed = scm.State != ScmState.NotInstalled,
            service_state = scm.State.ToString(),
            binary_path_ok = ScmClient.BinaryPathIs(scm, target.ServiceExe),
            runs_as_localsystem = string.Equals(scm.StartName, "LocalSystem", StringComparison.OrdinalIgnoreCase),
            auto_start = scm.StartType == 2,
            version = BuildVersion.Current,
            integrity_ok = problems.Count == 0,
            problems = problems.Take(20).ToArray(),
            recovery_record_present = store.MarkerExists,
            core_processes = Process.GetProcessesByName("asterlink-core").Length,
        });
    }

    // ---- shared helpers --------------------------------------------------------------------------

    private static string Summarise(IEnumerable<string> problems)
    {
        var list = problems.ToList();
        return string.Join("; ", list.Take(5)) + (list.Count > 5 ? $"; and {list.Count - 5} more" : "");
    }

    private static bool IsLink(string path)
    {
        var info = new DirectoryInfo(path);
        return info.LinkTarget is not null || (info.Attributes & FileAttributes.ReparsePoint) != 0;
    }

    private static void PrepareProgramData()
    {
        var directory = RecoveryStore.DefaultDirectory;
        if (Directory.Exists(directory) && IsLink(directory)) throw new InvalidOperationException("the ProgramData directory is a reparse point");
        Directory.CreateDirectory(directory);
        ProtectedFiles.Protect(directory, usersMayRead: false);
        RecoveryStore.Default().EnsureRunDirectory();
    }

    /// <summary>Removes only what this product installs; an unrelated file in the folder is left alone.</summary>
    private static void RemoveTree(InstallLayout target)
    {
        if (!Directory.Exists(target.Root)) return;
        if (IsLink(target.Root)) throw new InvalidOperationException("the install directory is a reparse point; it is left untouched");
        foreach (var name in InstallLayout.ManagedDirectories)
        {
            var directory = target.Join(name);
            if (!Directory.Exists(directory)) continue;
            if (IsLink(directory)) Directory.Delete(directory); else Directory.Delete(directory, recursive: true);
        }
        File.Delete(target.BuildConfig);
        File.Delete(target.Manifest);
        if (!Directory.EnumerateFileSystemEntries(target.Root).Any()) Directory.Delete(target.Root);
    }

    private static void CopyTree(string source, string destination)
    {
        Directory.CreateDirectory(destination);
        foreach (var entry in new DirectoryInfo(source).EnumerateFileSystemInfos())
        {
            if (entry.LinkTarget is not null || (entry.Attributes & FileAttributes.ReparsePoint) != 0)
                throw new InvalidOperationException("the stage contains a link: " + entry.Name);
            var target = Path.Combine(destination, entry.Name);
            if (entry is DirectoryInfo directory) CopyTree(directory.FullName, target);
            else File.Copy(entry.FullName, target);
        }
    }

    private static string Sc(params string[] arguments)
    {
        var start = new ProcessStartInfo(Path.Combine(Environment.SystemDirectory, "sc.exe"))
        {
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true,
        };
        foreach (var argument in arguments) start.ArgumentList.Add(argument);
        using var process = Process.Start(start) ?? throw new InvalidOperationException("sc.exe did not start");
        var output = process.StandardOutput.ReadToEnd() + process.StandardError.ReadToEnd();
        if (!process.WaitForExit(30_000)) { process.Kill(); throw new InvalidOperationException("sc.exe timed out"); }
        if (process.ExitCode != 0) throw new InvalidOperationException($"sc {arguments[0]} failed ({process.ExitCode}): {output.Trim()}");
        return output;
    }

    private static void CreateService(InstallLayout target)
    {
        Sc("create", ServiceConstants.ServiceName, "binPath=", Quoted(target.ServiceExe), "start=", "auto", "obj=", "LocalSystem",
            "DisplayName=", ServiceConstants.DisplayName);
        Sc("description", ServiceConstants.ServiceName, ServiceConstants.Description);
        // Restart after a crash; the restarted service only recovers, it never reconnects anyone.
        Sc("failure", ServiceConstants.ServiceName, "reset=", "86400", "actions=", "restart/5000/restart/10000/restart/30000");
    }

    private static void ConfigureService(InstallLayout target) =>
        Sc("config", ServiceConstants.ServiceName, "binPath=", Quoted(target.ServiceExe), "start=", "auto", "obj=", "LocalSystem");

    private static string Quoted(string path) => "\"" + path + "\"";

    private static void StopService()
    {
        try { Sc("stop", ServiceConstants.ServiceName); } catch (InvalidOperationException) { /* already stopping */ }
        WaitFor(s => s == ScmState.Stopped || s == ScmState.NotInstalled, TimeSpan.FromSeconds(40), "the service did not stop");
    }

    private static void RemoveService()
    {
        var state = ScmClient.Query().State;
        if (state == ScmState.NotInstalled) return;
        if (state != ScmState.Stopped) StopService();
        Sc("delete", ServiceConstants.ServiceName);
        WaitFor(s => s == ScmState.NotInstalled, TimeSpan.FromSeconds(20), "the service is still registered");
    }

    private static void StartAndWait()
    {
        ScmClient.Start();
        WaitFor(s => s == ScmState.Running, TimeSpan.FromSeconds(30), "the service did not reach Running");
    }

    private static void WaitFor(Func<ScmState, bool> done, TimeSpan timeout, string failure)
    {
        var deadline = DateTime.UtcNow + timeout;
        while (DateTime.UtcNow < deadline)
        {
            if (done(ScmClient.Query().State)) return;
            Thread.Sleep(250);
        }
        throw new InvalidOperationException(failure);
    }
}

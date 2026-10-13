using AsterLink.Shared.Service;

namespace AsterLink.Platform;

/// <summary>
/// The persisted ownership record under %ProgramData%\AsterLink (SYSTEM and Administrators only).
/// Writes are atomic and flushed to the device before the Core is allowed to run.
/// </summary>
public sealed class RecoveryStore(string directory)
{
    public static string DefaultDirectory =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), ServiceConstants.ProductDirectory);

    public static RecoveryStore Default() => new(DefaultDirectory);

    public string Directory { get; } = directory;
    public string MarkerPath => Path.Combine(Directory, ServiceConstants.SessionMarkerFile);
    public string RunDirectory => Path.Combine(Directory, "run");

    public bool MarkerExists => File.Exists(MarkerPath);

    public void Write(SessionMarker marker)
    {
        System.IO.Directory.CreateDirectory(Directory);
        var temporary = MarkerPath + ".tmp";
        using (var stream = new FileStream(temporary, FileMode.Create, FileAccess.Write, FileShare.None, 4096, FileOptions.WriteThrough))
        {
            var bytes = marker.ToJson();
            stream.Write(bytes, 0, bytes.Length);
            stream.Flush(flushToDisk: true);
        }
        File.Move(temporary, MarkerPath, overwrite: true);
    }

    /// <summary>Marker content, or null if the file is absent or unparseable (check <see cref="MarkerExists"/> to tell which).</summary>
    public SessionMarker? Read()
    {
        try { return File.Exists(MarkerPath) ? SessionMarker.TryParse(File.ReadAllBytes(MarkerPath)) : null; }
        catch (IOException) { return null; }
        catch (UnauthorizedAccessException) { return null; }
    }

    public void Delete()
    {
        try { File.Delete(MarkerPath); } catch (DirectoryNotFoundException) { }
        try { File.Delete(MarkerPath + ".tmp"); } catch (DirectoryNotFoundException) { }
    }

    /// <summary>Moves an unreadable record aside instead of deleting it, so the evidence survives the repair.</summary>
    public string? ArchiveCorrupt()
    {
        if (!MarkerExists) return null;
        var target = Path.Combine(Directory, $"session.corrupt-{DateTime.UtcNow:yyyyMMddHHmmss}.json");
        File.Move(MarkerPath, target);
        return target;
    }

    /// <summary>Empties the Core's private temp directory. Only ever called on the product's own run directory.</summary>
    public void CleanRunDirectory()
    {
        if (!System.IO.Directory.Exists(RunDirectory)) return;
        foreach (var entry in new DirectoryInfo(RunDirectory).EnumerateFileSystemInfos())
        {
            // A link inside the run directory is removed as a link, never followed.
            try
            {
                if (entry.LinkTarget is not null || (entry.Attributes & FileAttributes.ReparsePoint) != 0) entry.Delete();
                else if (entry is DirectoryInfo sub) sub.Delete(recursive: true);
                else entry.Delete();
            }
            catch (IOException) { }
            catch (UnauthorizedAccessException) { }
        }
    }

    public void EnsureRunDirectory() => System.IO.Directory.CreateDirectory(RunDirectory);
}

public sealed record RecoveryOutcome(RecoveryAction Action, bool Blocked, string Detail);

public static class StartupRecovery
{
    /// <summary>
    /// Runs once when the service starts. It restores nothing about authorisation: it only makes sure
    /// no Core this product started earlier is still alive, then clears the record. Whatever it cannot
    /// explain it leaves alone and blocks connecting until the repair verb archives it.
    /// </summary>
    public static RecoveryOutcome Run(RecoveryStore store)
    {
        var present = store.MarkerExists;
        var marker = present ? store.Read() : null;
        RunningProcess? running = null;
        if (marker is not null && ProcessIdentity.Read(marker.CoreProcessId) is { } facts)
            running = new RunningProcess(facts.StartFileTime, facts.ImagePath);

        var action = RecoveryPolicy.Decide(present, marker, running);
        switch (action)
        {
            case RecoveryAction.Block:
                return new RecoveryOutcome(action, true, "the recovery record is unreadable; run repair to archive it");
            case RecoveryAction.KillThenClear:
                if (!ProcessIdentity.TryTerminate(marker!.CoreProcessId, marker.CoreStartFileTime))
                    return new RecoveryOutcome(action, true, "a previous Core is still running and could not be stopped");
                store.Delete();
                store.CleanRunDirectory();
                return new RecoveryOutcome(action, false, "stopped a Core left over from an earlier run");
            case RecoveryAction.ClearOnly:
                store.Delete();
                store.CleanRunDirectory();
                return new RecoveryOutcome(action, false, "cleared the record of a Core that had already exited");
            default:
                store.CleanRunDirectory();
                return new RecoveryOutcome(action, false, "nothing to recover");
        }
    }
}

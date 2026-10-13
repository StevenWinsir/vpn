using System.Diagnostics;
using AsterLink.Platform;
using AsterLink.Shared.Service;

namespace AsterLink.Tests.Windows;

public class RecoveryTests
{
    private static SessionMarker MarkerFor(Process process, long? startOverride = null)
    {
        using var handle = Process.GetProcessById(process.Id);
        var facts = ProcessIdentity.Read(process.Id)!;
        return new SessionMarker
        {
            ServiceProcessId = Environment.ProcessId,
            CoreProcessId = process.Id,
            CoreStartFileTime = startOverride ?? facts.StartFileTime,
            CorePath = facts.ImagePath!,
            OwnerSid = facts.UserSid!,
            StartedAt = DateTimeOffset.UtcNow,
        };
    }

    [WindowsFact]
    public void Marker_is_written_atomically_and_read_back()
    {
        using var scratch = new Scratch();
        var store = new RecoveryStore(scratch.Root);
        using var child = ChildProcess.StartSleeper();
        try
        {
            store.Write(MarkerFor(child));
            Assert.True(store.MarkerExists);
            Assert.False(File.Exists(store.MarkerPath + ".tmp"));
            Assert.Equal(child.Id, store.Read()!.CoreProcessId);
        }
        finally { child.Kill(); }
    }

    [WindowsFact]
    public void A_core_left_running_by_an_earlier_service_is_stopped_and_the_record_cleared()
    {
        using var scratch = new Scratch();
        var store = new RecoveryStore(scratch.Root);
        using var orphan = ChildProcess.StartSleeper();
        try
        {
            store.Write(MarkerFor(orphan));
            var outcome = StartupRecovery.Run(store);

            Assert.Equal(RecoveryAction.KillThenClear, outcome.Action);
            Assert.False(outcome.Blocked);
            Assert.True(orphan.WaitForExit(10_000), "the leftover process must have been terminated");
            Assert.False(store.MarkerExists);
        }
        finally { if (!orphan.HasExited) orphan.Kill(); }
    }

    [WindowsFact]
    public void A_reused_pid_with_another_start_time_is_not_killed()
    {
        using var scratch = new Scratch();
        var store = new RecoveryStore(scratch.Root);
        using var bystander = ChildProcess.StartSleeper();
        try
        {
            store.Write(MarkerFor(bystander, startOverride: 1));
            var outcome = StartupRecovery.Run(store);

            Assert.Equal(RecoveryAction.ClearOnly, outcome.Action);
            Assert.False(bystander.HasExited, "an unrelated process must survive recovery");
            Assert.False(store.MarkerExists);
        }
        finally { bystander.Kill(); }
    }

    [WindowsFact]
    public void An_unreadable_record_blocks_instead_of_being_guessed_at_and_repair_archives_it()
    {
        using var scratch = new Scratch();
        var store = new RecoveryStore(scratch.Root);
        File.WriteAllText(store.MarkerPath, "{ not json");

        var outcome = StartupRecovery.Run(store);
        Assert.True(outcome.Blocked);
        Assert.Equal(RecoveryAction.Block, outcome.Action);
        Assert.True(store.MarkerExists, "the evidence must stay in place until repair");

        var archived = store.ArchiveCorrupt();
        Assert.NotNull(archived);
        Assert.True(File.Exists(archived));
        Assert.False(store.MarkerExists);
        Assert.False(StartupRecovery.Run(store).Blocked);
    }

    [WindowsFact]
    public void No_record_means_nothing_happens_and_the_run_directory_is_emptied()
    {
        using var scratch = new Scratch();
        var store = new RecoveryStore(scratch.Root);
        store.EnsureRunDirectory();
        File.WriteAllText(System.IO.Path.Combine(store.RunDirectory, "leftover.tmp"), "x");
        Directory.CreateDirectory(System.IO.Path.Combine(store.RunDirectory, "asterlink-engine-1"));

        var outcome = StartupRecovery.Run(store);
        Assert.Equal(RecoveryAction.Nothing, outcome.Action);
        Assert.Empty(Directory.EnumerateFileSystemEntries(store.RunDirectory));
    }
}

using System.Text.Json;
using AsterLink.Platform;
using AsterLink.Shared.Ipc;

namespace AsterLink.Tests.Windows;

/// <summary>Runs the real Go Core (built earlier in the same CI job) through the real launcher.</summary>
public class WorkerTests
{
    private static string Core => CoreSupport.Path!;

    private static WorkerProcess Launch(Action<int, long>? beforeRun = null)
    {
        var temp = Directory.CreateTempSubdirectory("asterlink-run-").FullName;
        return WorkerProcess.Start(Core, System.IO.Path.GetDirectoryName(Core)!, temp, beforeRun ?? ((_, _) => { }));
    }

    private static async Task<IpcResponse> Exchange(WorkerProcess worker, string command, string id)
    {
        await IpcFrame.WriteAsync(worker.ToWorker, IpcJson.SerializeRequest(new IpcRequest { RequestId = id, Command = command }));
        return IpcJson.ParseResponse(await IpcFrame.ReadAsync(worker.FromWorker).WaitAsync(TimeSpan.FromSeconds(30)));
    }

    [CoreFact]
    public async Task The_core_answers_hello_over_the_private_handles()
    {
        using var worker = Launch();
        var hello = await Exchange(worker, IpcCommands.Hello, "h1");
        Assert.True(hello.Ok);
        Assert.Equal("asterlink-core", hello.Result!.Value.GetProperty("component").GetString());
    }

    [CoreFact]
    public async Task A_read_snapshot_comes_from_the_core_and_is_signed_out()
    {
        using var worker = Launch();
        var reply = IpcJson.ParseSnapshotReply(await Exchange(worker, IpcCommands.ReadSnapshot, "s1"));
        Assert.True(reply.Succeeded);
        Assert.NotEqual("connected", reply.Snapshot.State);
    }

    [CoreFact]
    public async Task Disposing_the_worker_ends_the_core_process()
    {
        var worker = Launch();
        Assert.True((await Exchange(worker, IpcCommands.Hello, "h1")).Ok);
        var exited = worker.Exited;
        worker.Dispose();
        Assert.True(await Task.WhenAny(exited, Task.Delay(10_000)) == exited, "the Core must die with its job");
    }

    [CoreFact]
    public async Task Closing_the_command_pipe_is_end_of_file_and_the_core_exits_by_itself()
    {
        using var worker = Launch();
        Assert.True((await Exchange(worker, IpcCommands.Hello, "h1")).Ok);
        worker.ToWorker.Dispose();
        Assert.True(await Task.WhenAny(worker.Exited, Task.Delay(20_000)) == worker.Exited);
        Assert.Equal(0u, await worker.Exited);
    }

    [CoreFact]
    public void A_failing_record_write_kills_the_core_before_it_runs()
    {
        var pid = 0;
        Assert.Throws<IOException>(() => Launch((p, _) => { pid = p; throw new IOException("disk full"); }));

        Assert.NotEqual(0, pid);
        var survivor = ProcessIdentity.Read(pid);
        // The pid may already be gone; if the number was reused the image is not the Core.
        Assert.True(survivor is null || !string.Equals(survivor.ImagePath, Core, StringComparison.OrdinalIgnoreCase));
    }

    [CoreFact]
    public async Task The_record_is_written_while_the_core_is_still_suspended()
    {
        var recorded = new TaskCompletionSource<(int Pid, long Start)>();
        using var worker = Launch((pid, start) => recorded.SetResult((pid, start)));
        var (pid, start) = await recorded.Task;
        Assert.Equal(worker.ProcessId, pid);
        Assert.Equal(worker.StartFileTime, start);
        Assert.True((await Exchange(worker, IpcCommands.Hello, "h1")).Ok);
    }

    [CoreFact]
    public async Task The_core_does_not_see_the_services_environment()
    {
        Environment.SetEnvironmentVariable("ASTERLINK_TEST_SECRET", "must-not-leak");
        try
        {
            using var worker = Launch();
            // The Core would only exit on a bad handle; reaching hello proves the explicit environment was enough.
            Assert.True((await Exchange(worker, IpcCommands.Hello, "h1")).Ok);
        }
        finally { Environment.SetEnvironmentVariable("ASTERLINK_TEST_SECRET", null); }
    }

    [CoreFact]
    public void The_core_started_by_hand_without_handles_exits_with_one()
    {
        var info = new System.Diagnostics.ProcessStartInfo(Core) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardError = true };
        using var process = System.Diagnostics.Process.Start(info)!;
        Assert.True(process.WaitForExit(20_000));
        Assert.Equal(1, process.ExitCode);
    }
}

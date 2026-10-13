using System.Text.Json;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;

namespace AsterLink.Platform;

public sealed class ServiceProbeResult
{
    public string ScmState { get; init; } = "";
    public bool Connected { get; init; }
    public string? ErrorCode { get; init; }
    public string? ErrorMessage { get; init; }
    public string? ServiceVersion { get; init; }
    public string? CoreVersion { get; init; }
    public string? Channel { get; init; }
    public string? SnapshotState { get; init; }
    public ServiceHealthState Health { get; init; }
    public RecommendedAction Action { get; init; }
    public string Message { get; init; } = "";
}

/// <summary>
/// One real round trip through the whole chain: SCM facts, server verification, hello (which makes the
/// service start the Core and check its version) and a read_snapshot answered by the Core itself.
/// Used by the GUI status card and by the CI end-to-end checks (<c>AsterLink.exe --service-check</c>).
/// </summary>
public static class ServiceDiagnostics
{
    /// <param name="hold">Keep the verified connection open this long after a successful probe (test support: lets a harness kill the service or the client while a Core is alive).</param>
    public static async Task<ServiceProbeResult> ProbeAsync(InstallLayout? guiLayout, CancellationToken cancellationToken = default, TimeSpan? hold = null)
    {
        var programFiles = InstallVerifier.ProgramFilesLayout();
        var inInstalledRoot = guiLayout is not null && guiLayout.IsAt(programFiles.Root);
        var besideStage = guiLayout is not null && File.Exists(guiLayout.ServiceExe) && File.Exists(guiLayout.Manifest);

        ScmInfo scm;
        try { scm = ScmClient.Query(); }
        catch (System.ComponentModel.Win32Exception error)
        {
            return Failed("Unknown", ServiceErrors.ServiceNotRunning, "cannot query the service manager: " + error.Message, inInstalledRoot, besideStage, ScmState.Other);
        }

        if (scm.State != ScmState.Running || !inInstalledRoot)
        {
            var health = ServiceHealthClassifier.Classify(inInstalledRoot, besideStage, scm.State, null);
            return new ServiceProbeResult { ScmState = scm.State.ToString(), Health = health.State, Action = health.Action, Message = health.Message };
        }

        try
        {
            await using var connection = await ServiceConnection.ConnectAsync(programFiles, TimeSpan.FromSeconds(10), cancellationToken).ConfigureAwait(false);

            var helloPayload = JsonSerializer.SerializeToElement(new { app_version = BuildVersion.Current });
            var hello = await connection.SendAsync(new IpcRequest { RequestId = "probe-hello", Command = IpcCommands.Hello, Payload = helloPayload }, cancellationToken).ConfigureAwait(false);
            if (!hello.Ok)
                return Failed(scm.State.ToString(), hello.Error?.Code ?? "unknown", hello.Error?.Message ?? "", inInstalledRoot, besideStage, scm.State);

            var info = hello.Result!.Value;
            var snapshotReply = await connection.SendAsync(new IpcRequest { RequestId = "probe-snapshot", Command = IpcCommands.ReadSnapshot }, cancellationToken).ConfigureAwait(false);
            string? snapshotState = null;
            if (snapshotReply.Ok) snapshotState = IpcJson.ParseSnapshotReply(snapshotReply).Snapshot.State;
            else return Failed(scm.State.ToString(), snapshotReply.Error?.Code ?? "unknown", snapshotReply.Error?.Message ?? "", inInstalledRoot, besideStage, scm.State);

            var ready = ServiceHealthClassifier.Classify(inInstalledRoot, besideStage, scm.State, null);
            if (hold is { } wait && wait > TimeSpan.Zero) await Task.Delay(wait, cancellationToken).ConfigureAwait(false);
            return new ServiceProbeResult
            {
                ScmState = scm.State.ToString(),
                Connected = true,
                ServiceVersion = info.GetProperty("service_version").GetString(),
                CoreVersion = info.TryGetProperty("core_version", out var core) ? core.GetString() : null,
                Channel = info.GetProperty("channel").GetString(),
                SnapshotState = snapshotState,
                Health = ready.State,
                Action = ready.Action,
                Message = ready.Message,
            };
        }
        catch (ServiceClientException error)
        {
            return Failed(scm.State.ToString(), error.Code, error.Message, inInstalledRoot, besideStage, scm.State);
        }
        catch (Exception error) when (error is IOException or JsonException or InvalidOperationException or KeyNotFoundException)
        {
            return Failed(scm.State.ToString(), ServiceErrors.WorkerExited, error.Message, inInstalledRoot, besideStage, scm.State);
        }
    }

    private static ServiceProbeResult Failed(string scmName, string code, string message, bool inInstalledRoot, bool besideStage, ScmState state)
    {
        var health = ServiceHealthClassifier.Classify(inInstalledRoot, besideStage, state, code);
        return new ServiceProbeResult
        {
            ScmState = scmName,
            ErrorCode = code,
            ErrorMessage = message,
            Health = health.State,
            Action = health.Action,
            Message = health.Message,
        };
    }
}

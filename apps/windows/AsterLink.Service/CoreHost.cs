using System.Text.Json;
using AsterLink.Platform;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;
using Microsoft.Extensions.Logging;

namespace AsterLink.Service;

public sealed class WorkerLaunchException(string code, string message) : Exception(message)
{
    public string Code { get; } = code;
}

/// <summary>The Core went away, or stopped answering, while a request was in flight.</summary>
public sealed class WorkerLostException(string code, string message) : Exception(message)
{
    public string Code { get; } = code;
}

/// <summary>
/// Owns at most one Core for the owner session: verifies the install, locks and hashes the Core
/// file, starts it inside the kill-on-close job, checks its identity with a real hello, relays
/// frames, and stops it. It never interprets business commands; the Core stays the only owner of
/// login, catalog, metering and tunnel state.
/// </summary>
public sealed class CoreHost(InstallLayout layout, RecoveryStore store, ILogger logger)
{
    private FileStream? coreLease;
    private WorkerProcess? worker;

    public string? CoreVersion { get; private set; }
    public string? MihomoRevision { get; private set; }
    public bool IsRunning => worker is { Exited.IsCompleted: false };

    /// <summary>Starts a Core if none is alive. Throws <see cref="WorkerLaunchException"/> with a stable code.</summary>
    public async Task EnsureStartedAsync(string ownerSid, ReleaseManifest manifest, CancellationToken cancellationToken)
    {
        if (IsRunning) return;
        await StopAsync().ConfigureAwait(false);

        var report = await Task.Run(() => InstallVerifier.Verify(layout), cancellationToken).ConfigureAwait(false);
        if (!report.Trusted)
        {
            logger.LogWarning("Install verification failed before Core launch: {Count} problem(s)", report.Problems.Count);
            throw new WorkerLaunchException(ServiceErrors.IntegrityFailed, "installed files do not match the manifest");
        }

        try
        {
            // Deny-write sharing keeps the file we hash identical to the file that runs.
            coreLease = new FileStream(layout.CoreExe, FileMode.Open, FileAccess.Read, FileShare.Read);
            var hash = ReleaseManifest.HashStream(coreLease);
            if (!string.Equals(hash, manifest.Role("core").Sha256, StringComparison.Ordinal))
                throw new WorkerLaunchException(ServiceErrors.IntegrityFailed, "the Core does not match the manifest");

            store.EnsureRunDirectory();
            var started = DateTimeOffset.UtcNow;
            worker = WorkerProcess.Start(layout.CoreExe, layout.Join("core"), store.RunDirectory, (pid, startTime) =>
                store.Write(new SessionMarker
                {
                    ServiceProcessId = Environment.ProcessId,
                    CoreProcessId = pid,
                    CoreStartFileTime = startTime,
                    CorePath = layout.CoreExe,
                    OwnerSid = ownerSid,
                    StartedAt = started,
                }));
        }
        catch (WorkerLaunchException) { await StopAsync().ConfigureAwait(false); throw; }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or System.ComponentModel.Win32Exception)
        {
            logger.LogError("Core could not be started: {Message}", error.Message);
            await StopAsync().ConfigureAwait(false);
            throw new WorkerLaunchException(ServiceErrors.WorkerUnavailable, "the Core could not be started");
        }

        try { await CheckCoreIdentityAsync(manifest, cancellationToken).ConfigureAwait(false); }
        catch
        {
            await StopAsync().ConfigureAwait(false);
            throw;
        }
        logger.LogInformation("Core {Version} started (pid {Pid})", CoreVersion, worker!.ProcessId);
    }

    private async Task CheckCoreIdentityAsync(ReleaseManifest manifest, CancellationToken cancellationToken)
    {
        var hello = IpcJson.SerializeRequest(new IpcRequest { RequestId = "service-hello", Command = IpcCommands.Hello });
        byte[] reply;
        try
        {
            using var limit = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            limit.CancelAfter(TimeSpan.FromSeconds(15));
            reply = await ExchangeAsync(hello, "service-hello", limit.Token, TimeSpan.FromSeconds(15)).ConfigureAwait(false);
        }
        catch (WorkerLostException error)
        {
            throw new WorkerLaunchException(ServiceErrors.WorkerUnavailable, error.Message);
        }

        var response = IpcJson.ParseResponse(reply);
        if (!response.Ok || response.Result is not { } result)
            throw new WorkerLaunchException(ServiceErrors.WorkerUnavailable, "the Core did not answer hello");
        var component = result.GetProperty("component").GetString();
        var version = result.GetProperty("core_version").GetString();
        var protocol = result.GetProperty("protocol_version").GetInt32();
        if (component != "asterlink-core" || protocol != IpcProtocol.Version)
            throw new WorkerLaunchException(ServiceErrors.VersionMismatch, "the Core speaks a different protocol");
        if (!VersionGate.Check(manifest.Version, BuildVersion.Current, null, version).Compatible)
            throw new WorkerLaunchException(ServiceErrors.VersionMismatch, "the Core version differs from the installed release");
        CoreVersion = version;
        MihomoRevision = result.TryGetProperty("mihomo_revision", out var revision) ? revision.GetString() : null;
    }

    /// <summary>Sends one frame to the Core and returns the matching reply frame.</summary>
    public async Task<byte[]> ExchangeAsync(byte[] request, string requestId, CancellationToken cancellationToken, TimeSpan? timeout = null)
    {
        var current = worker ?? throw new WorkerLostException(ServiceErrors.WorkerExited, "the Core is not running");
        var deadline = timeout ?? ServiceConstants.WorkerReplyTimeout;
        try
        {
            await IpcFrame.WriteAsync(current.ToWorker, request, cancellationToken).ConfigureAwait(false);

            // Pipe reads on the Core's anonymous pipe cannot be cancelled; the only way to end a read
            // is the pipe closing, so a hung or abandoned Core is killed rather than waited for.
            var read = IpcFrame.ReadAsync(current.FromWorker, IpcFrame.MaxFrame);
            var finished = await Task.WhenAny(read, Task.Delay(deadline, cancellationToken)).ConfigureAwait(false);
            if (finished != read)
            {
                current.Kill();
                await SwallowAsync(read).ConfigureAwait(false);
                cancellationToken.ThrowIfCancellationRequested();
                throw new WorkerLostException(ServiceErrors.WorkerUnresponsive, "the Core did not answer in time");
            }

            var frame = await read.ConfigureAwait(false);
            if (!ReplyMatches(frame, requestId))
            {
                // A reply that is not ours means the stream can no longer be trusted: stop the Core.
                current.Kill();
                throw new WorkerLostException(ServiceErrors.WorkerExited, "the Core sent an unexpected reply");
            }
            return frame;
        }
        catch (IpcFrameException)
        {
            current.Kill();
            throw new WorkerLostException(ServiceErrors.WorkerExited, "the Core connection ended");
        }
        catch (IOException)
        {
            current.Kill();
            throw new WorkerLostException(ServiceErrors.WorkerExited, "the Core connection ended");
        }
        catch (ObjectDisposedException)
        {
            throw new WorkerLostException(ServiceErrors.WorkerExited, "the Core connection ended");
        }
    }

    private static bool ReplyMatches(byte[] frame, string requestId)
    {
        try
        {
            using var document = JsonDocument.Parse(frame);
            return document.RootElement.TryGetProperty("request_id", out var id) && id.GetString() == requestId;
        }
        catch (JsonException) { return false; }
    }

    private static async Task SwallowAsync(Task task)
    {
        try { await task.ConfigureAwait(false); }
        catch (Exception) { /* the read ends with an error once the Core is killed: expected */ }
    }

    /// <summary>
    /// Graceful first: closing the command pipe is end-of-file for the Core, which then settles its
    /// session and exits. After the grace period the job is terminated. The record is cleared only
    /// once the process is really gone.
    /// </summary>
    public async Task StopAsync()
    {
        var current = worker;
        var lease = coreLease;
        worker = null;
        coreLease = null;
        if (current is not null)
        {
            try { current.ToWorker.Dispose(); } catch (IOException) { }
            var finished = await Task.WhenAny(current.Exited, Task.Delay(ServiceConstants.WorkerStopGrace)).ConfigureAwait(false);
            if (finished != current.Exited)
            {
                logger.LogWarning("Core did not stop within the grace period; terminating its job");
                current.Kill();
                await Task.WhenAny(current.Exited, Task.Delay(TimeSpan.FromSeconds(5))).ConfigureAwait(false);
            }
            var gone = current.Exited.IsCompleted;
            current.Dispose();
            if (gone)
            {
                store.Delete();
                store.CleanRunDirectory();
            }
            else logger.LogError("Core is still alive after termination; the recovery record is kept");
        }
        lease?.Dispose();
        CoreVersion = null;
        MihomoRevision = null;
    }
}

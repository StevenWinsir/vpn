using System.IO.Pipes;
using System.Text.Json;
using System.Threading.Channels;
using AsterLink.Platform;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;
using Microsoft.Extensions.Logging;

namespace AsterLink.Service;

/// <summary>
/// One accepted GUI connection. The first frame must be a valid <c>hello</c>; only after the caller's
/// identity, the install's integrity and the version set have all been checked does the service claim
/// the owner slot, start a Core, and answer. Every later request is checked against the command policy
/// before it is relayed. Nothing the GUI sends is ever interpreted as a path, a command line or a config.
/// </summary>
public sealed class ClientSession(NamedPipeServerStream pipe, ServiceRuntime runtime) : IOwnerConnection
{
    private readonly CoreHost host = new(runtime.Layout, runtime.Store, runtime.Logger);
    private readonly CancellationTokenSource cancel = new();
    private Task completion = Task.CompletedTask;
    private bool ownsSlot;
    private string ownerSid = "";

    public Task Completion => completion;

    public Task RunAsync(CancellationToken serviceStopping)
    {
        var linked = CancellationTokenSource.CreateLinkedTokenSource(serviceStopping, cancel.Token);
        completion = Task.Run(() => RunCoreAsync(linked.Token));
        return completion;
    }

    public async Task CloseAsync()
    {
        await cancel.CancelAsync().ConfigureAwait(false);
        try { await completion.ConfigureAwait(false); } catch (Exception) { /* already logged by the session */ }
    }

    private async Task RunCoreAsync(CancellationToken token)
    {
        try
        {
            if (!await AdmitAsync(token).ConfigureAwait(false)) return;
            await ServeAsync(token).ConfigureAwait(false);
        }
        catch (OperationCanceledException) { }
        catch (Exception error) when (error is IOException or ObjectDisposedException or InvalidOperationException)
        {
            runtime.Logger.LogInformation("GUI connection ended: {Reason}", error.GetType().Name);
        }
        catch (Exception error)
        {
            runtime.Logger.LogError(error, "GUI session failed unexpectedly");
        }
        finally
        {
            await TearDownAsync().ConfigureAwait(false);
        }
    }

    /// <summary>Everything that has to be true before the first byte of a reply: identity, integrity, version, ownership, worker.</summary>
    private async Task<bool> AdmitAsync(CancellationToken token)
    {
        byte[] first;
        try { first = await IpcFrame.ReadAsync(pipe, 4 << 10, token).ConfigureAwait(false); }
        catch (IpcFrameException) { return false; } // not even a frame: nothing to answer

        // Cheap parse first: the expensive whole-tree hash is only spent on a caller that passes the identity check.
        var metadata = InstallVerifier.LoadMetadata(runtime.Layout);
        if (!metadata.Trusted)
        {
            runtime.Logger.LogWarning("Refused a GUI connection: install metadata is not usable ({Count} problem(s))", metadata.Problems.Count);
            await ReplyAsync(ServiceReplies.Error(TryRequestId(first), 0, ServiceErrors.IntegrityFailed, "installed files do not match the manifest; repair is required"), token).ConfigureAwait(false);
            return false;
        }

        var decision = CommandPolicy.Evaluate(first, metadata.BuildConfig!);
        if (!decision.Allowed || decision.Accepted!.Command != IpcCommands.Hello)
        {
            await ReplyAsync(ServiceReplies.Error(decision.RequestId, decision.Generation, decision.ErrorCode ?? ServiceErrors.InvalidRequest,
                decision.Allowed ? "the first request must be hello" : decision.Message ?? "malformed request"), token).ConfigureAwait(false);
            return false;
        }
        var hello = decision.Accepted;

        // Identity is measured only now: Windows allows impersonating a pipe client only after it has sent data.
        var peer = ServiceEndpoint.Inspect(pipe);
        var guiPath = runtime.Layout.AppExe;
        var verdict = PeerPolicy.Evaluate(new PeerFacts(
            peer.ProcessId, peer.PipeSessionId, peer.Process?.SessionId ?? -1,
            peer.Process?.UserSid, peer.ImpersonatedSid, peer.Process?.IntegrityRid ?? 0,
            peer.Process?.ImagePath, HashIfInstalled(peer.Process?.ImagePath, guiPath)),
            guiPath, metadata.Manifest!.Role("gui").Sha256);
        if (!verdict.Allowed)
        {
            runtime.Logger.LogWarning("Refused a GUI connection from pid {Pid}: {Verdict}", peer.ProcessId, verdict.Verdict);
            await ReplyAsync(ServiceReplies.Error(hello.RequestId, hello.Generation, ServiceErrors.PeerRejected, $"the caller is not the installed AsterLink application ({verdict.Verdict})"), token).ConfigureAwait(false);
            return false;
        }

        var report = await Task.Run(() => InstallVerifier.Verify(runtime.Layout), token).ConfigureAwait(false);
        if (!report.Trusted)
        {
            runtime.Logger.LogWarning("Refused a GUI connection: install verification found {Count} problem(s)", report.Problems.Count);
            await ReplyAsync(ServiceReplies.Error(hello.RequestId, hello.Generation, ServiceErrors.IntegrityFailed, "installed files do not match the manifest; repair is required"), token).ConfigureAwait(false);
            return false;
        }

        if (runtime.RecoveryBlocked)
        {
            await ReplyAsync(ServiceReplies.Error(hello.RequestId, hello.Generation, ServiceErrors.RecoveryBlocked, "an earlier session could not be recovered; repair is required"), token).ConfigureAwait(false);
            return false;
        }

        var guiVersion = AppVersionFrom(hello.Payload);
        var gate = guiVersion is null
            ? new VersionVerdict(false, "hello did not state the application version")
            : VersionGate.Check(report.Manifest!.Version, BuildVersion.Current, guiVersion, null);
        if (!gate.Compatible)
        {
            runtime.Logger.LogWarning("Refused a GUI connection: {Detail}", gate.Detail);
            await ReplyAsync(ServiceReplies.Error(hello.RequestId, hello.Generation, ServiceErrors.VersionMismatch, gate.Detail), token).ConfigureAwait(false);
            return false;
        }

        ownerSid = peer.Process!.UserSid!;
        var claim = runtime.Owners.TryClaim(ownerSid, peer.PipeSessionId, this);
        if (claim.Outcome == OwnerOutcome.Busy)
        {
            await ReplyAsync(ServiceReplies.Error(hello.RequestId, hello.Generation, ServiceErrors.OwnerBusy, "another Windows user is using AsterLink on this computer"), token).ConfigureAwait(false);
            return false;
        }
        ownsSlot = true;
        // The earlier connection of the same user must be completely gone (Core included) before this one starts its own.
        if (claim.Previous is { } previous) await previous.CloseAsync().ConfigureAwait(false);

        try { await host.EnsureStartedAsync(ownerSid, report.Manifest!, token).ConfigureAwait(false); }
        catch (WorkerLaunchException error)
        {
            await ReplyAsync(ServiceReplies.Error(hello.RequestId, hello.Generation, error.Code, error.Message), token).ConfigureAwait(false);
            return false;
        }

        await ReplyAsync(ServiceReplies.Ok(hello.RequestId, hello.Generation, HelloResult(report.Manifest!)), token).ConfigureAwait(false);
        buildConfig = report.BuildConfig;
        manifest = report.Manifest;
        return true;
    }

    private Shared.Env.ClientEnvironment? buildConfig;
    private ReleaseManifest? manifest;

    private ServiceHelloResult HelloResult(ReleaseManifest installed) => new()
    {
        ServiceVersion = BuildVersion.Current,
        CoreVersion = host.CoreVersion,
        MihomoRevision = host.MihomoRevision,
        Channel = installed.Channel,
    };

    private async Task ServeAsync(CancellationToken token)
    {
        // A separate reader notices the GUI going away even while a long command waits on the Core.
        var requests = Channel.CreateUnbounded<byte[]>();
        var gone = CancellationTokenSource.CreateLinkedTokenSource(token);
        _ = Task.Run(() => ReadLoopAsync(requests.Writer, gone), CancellationToken.None);

        try
        {
            while (await requests.Reader.WaitToReadAsync(gone.Token).ConfigureAwait(false))
            {
                while (requests.Reader.TryRead(out var frame))
                    if (!await HandleAsync(frame, gone.Token).ConfigureAwait(false)) return;
            }
            // Reader completed: either a clean close or a framing error to answer.
            await requests.Reader.Completion.ConfigureAwait(false);
        }
        catch (ChannelClosedException error) when (error.InnerException is IpcFrameException frameError)
        {
            await AnswerFramingErrorAsync(frameError, token).ConfigureAwait(false);
        }
        catch (IpcFrameException frameError)
        {
            await AnswerFramingErrorAsync(frameError, token).ConfigureAwait(false);
        }
    }

    private async Task AnswerFramingErrorAsync(IpcFrameException error, CancellationToken token)
    {
        if (error.Error is IpcFrameError.EndOfStream) return;
        var code = error.Error == IpcFrameError.TooLarge ? ServiceErrors.PayloadTooLarge : ServiceErrors.InvalidRequest;
        try { await ReplyAsync(ServiceReplies.Error("", 0, code, "request frame is not acceptable"), token).ConfigureAwait(false); }
        catch (Exception error2) when (error2 is IOException or ObjectDisposedException) { }
    }

    private async Task ReadLoopAsync(ChannelWriter<byte[]> writer, CancellationTokenSource gone)
    {
        Exception? failure = null;
        try
        {
            while (true)
                await writer.WriteAsync(await IpcFrame.ReadAsync(pipe, CommandPolicy.MaxRequestFrame, gone.Token).ConfigureAwait(false), gone.Token).ConfigureAwait(false);
        }
        catch (IpcFrameException error) when (error.Error != IpcFrameError.EndOfStream && error.Error != IpcFrameError.Truncated)
        {
            failure = error; // answer it, then close; the stream position can no longer be trusted
        }
        catch (Exception error) when (error is IpcFrameException or IOException or ObjectDisposedException or OperationCanceledException)
        {
            await gone.CancelAsync().ConfigureAwait(false); // the GUI is gone: stop waiting on the Core too
        }
        finally { writer.TryComplete(failure); }
    }

    private async Task<bool> HandleAsync(byte[] frame, CancellationToken token)
    {
        var decision = CommandPolicy.Evaluate(frame, buildConfig!);
        if (!decision.Allowed)
        {
            await ReplyAsync(ServiceReplies.Error(decision.RequestId, decision.Generation, decision.ErrorCode!, decision.Message!), token).ConfigureAwait(false);
            return true;
        }
        var request = decision.Accepted!;

        switch (request.Command)
        {
            case IpcCommands.Hello:
                await ReplyAsync(ServiceReplies.Ok(request.RequestId, request.Generation, HelloResult(manifest!)), token).ConfigureAwait(false);
                return true;
            case IpcCommands.Shutdown:
                await ReplyAsync(ServiceReplies.Ok(request.RequestId, request.Generation, new { }), token).ConfigureAwait(false);
                return false;
        }

        try
        {
            if (!host.IsRunning)
            {
                // The Core died since the last request (the GUI was told then): start a fresh one. It
                // knows nothing of the old login, so the GUI must sign in again; no token is carried over.
                var report = await Task.Run(() => InstallVerifier.Verify(runtime.Layout), token).ConfigureAwait(false);
                if (!report.Trusted) throw new WorkerLaunchException(ServiceErrors.IntegrityFailed, "installed files do not match the manifest; repair is required");
                await host.EnsureStartedAsync(ownerSid, report.Manifest!, token).ConfigureAwait(false);
            }
            var reply = await host.ExchangeAsync(frame, request.RequestId, token).ConfigureAwait(false);
            await WriteAsync(reply, token).ConfigureAwait(false);
        }
        catch (WorkerLaunchException error)
        {
            await ReplyAsync(ServiceReplies.Error(request.RequestId, request.Generation, error.Code, error.Message), token).ConfigureAwait(false);
        }
        catch (WorkerLostException error)
        {
            await host.StopAsync().ConfigureAwait(false);
            await ReplyAsync(ServiceReplies.Error(request.RequestId, request.Generation, error.Code, error.Message), token).ConfigureAwait(false);
        }
        return true;
    }

    private Task ReplyAsync(byte[] frame, CancellationToken token) => WriteAsync(frame, token);

    private async Task WriteAsync(byte[] frame, CancellationToken token)
    {
        await IpcFrame.WriteAsync(pipe, frame, token).ConfigureAwait(false);
    }

    private async Task TearDownAsync()
    {
        try { await host.StopAsync().ConfigureAwait(false); }
        catch (Exception error) { runtime.Logger.LogError(error, "Stopping the Core failed"); }
        if (ownsSlot) runtime.Owners.Release(this);
        try { pipe.Dispose(); } catch (IOException) { }
    }

    private static string TryRequestId(byte[] frame)
    {
        try
        {
            using var document = JsonDocument.Parse(frame);
            var id = document.RootElement.TryGetProperty("request_id", out var value) ? value.GetString() : null;
            return id is { Length: > 0 and <= 64 } ? id : "";
        }
        catch (Exception error) when (error is JsonException or InvalidOperationException) { return ""; }
    }

    private static string? AppVersionFrom(JsonElement? payload)
    {
        if (payload is not { ValueKind: JsonValueKind.Object } body) return null;
        return body.TryGetProperty("app_version", out var version) && version.ValueKind == JsonValueKind.String ? version.GetString() : null;
    }

    private static string? HashIfInstalled(string? imagePath, string guiPath)
    {
        if (imagePath is null || !WinPath.Equal(imagePath, guiPath)) return null;
        try { return ReleaseManifest.HashFile(imagePath); }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException) { return null; }
    }
}

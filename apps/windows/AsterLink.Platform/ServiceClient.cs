using System.ComponentModel;
using System.Diagnostics;
using System.IO.Pipes;
using System.Security.Principal;
using System.Text.Json;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;

namespace AsterLink.Platform;

public sealed class ServiceClientException(string code, string message) : Exception(message)
{
    public string Code { get; } = code;
}

/// <summary>
/// The GUI side of the pipe. Before a single byte is sent it proves the other end is the real
/// service: the pipe's server PID must be the process the SCM reports for AsterLinkService, that
/// process must run as SYSTEM from the protected install path, and the SCM entry must point at that
/// same quoted path. A look-alike server that grabbed the pipe name first would fail this and
/// never see a password. The client also connects at Identification level, so even a fake server
/// could not act as the user.
/// </summary>
public sealed class ServiceConnection : IAsyncDisposable
{
    private readonly NamedPipeClientStream pipe;
    private readonly SemaphoreSlim exchange = new(1, 1);

    private ServiceConnection(NamedPipeClientStream pipe) => this.pipe = pipe;

    public static async Task<ServiceConnection> ConnectAsync(InstallLayout layout, TimeSpan timeout, CancellationToken cancellationToken = default)
    {
        var scm = ScmClient.Query();
        if (scm.State == ScmState.NotInstalled) throw new ServiceClientException(ServiceErrors.ServiceNotInstalled, "the AsterLink service is not installed");
        if (scm.State != ScmState.Running) throw new ServiceClientException(ServiceErrors.ServiceNotRunning, "the AsterLink service is not running");

        var pipe = new NamedPipeClientStream(".", ServiceConstants.PipeName, PipeDirection.InOut, PipeOptions.Asynchronous, TokenImpersonationLevel.Identification);
        try
        {
            using var limit = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            limit.CancelAfter(timeout);
            try { await pipe.ConnectAsync(limit.Token).ConfigureAwait(false); }
            catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
            {
                throw new ServiceClientException(ServiceErrors.ServiceNotRunning, "the service did not accept the connection in time");
            }
            if (VerifyServer(pipe, layout) is { } reason)
                throw new ServiceClientException(ServiceErrors.ServerRejected, reason);
            return new ServiceConnection(pipe);
        }
        catch
        {
            await pipe.DisposeAsync().ConfigureAwait(false);
            throw;
        }
    }

    /// <summary>Null when the server is the genuine service, otherwise the reason it was rejected.</summary>
    public static string? VerifyServer(NamedPipeClientStream pipe, InstallLayout layout)
    {
        if (!Native.GetNamedPipeServerProcessId(pipe.SafePipeHandle, out var serverPid) || serverPid == 0)
            return "cannot identify the pipe server";

        var scm = ScmClient.Query();
        if (scm.State != ScmState.Running || scm.ProcessId != (int)serverPid)
            return "the pipe is not served by the registered AsterLink service process";
        if (!ScmClient.BinaryPathIs(scm, layout.ServiceExe))
            return "the registered service does not point at the protected install path";
        if (!string.Equals(scm.StartName, "LocalSystem", StringComparison.OrdinalIgnoreCase))
            return "the registered service does not run as LocalSystem";

        var facts = ProcessIdentity.Read((int)serverPid);
        if (facts is null) return "cannot inspect the pipe server process";
        if (!WinPath.Equal(facts.ImagePath, layout.ServiceExe)) return "the pipe server is not running from the protected install path";
        if (!string.Equals(facts.UserSid, new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null).Value, StringComparison.Ordinal))
            return "the pipe server is not running as SYSTEM";
        return null;
    }

    /// <summary>One request, one response. A reply for another request id is a protocol violation, not data.</summary>
    public async Task<IpcResponse> SendAsync(IpcRequest request, CancellationToken cancellationToken = default)
    {
        await exchange.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await IpcFrame.WriteAsync(pipe, IpcJson.SerializeRequest(request), cancellationToken).ConfigureAwait(false);
            var frame = await IpcFrame.ReadAsync(pipe, IpcFrame.MaxFrame, cancellationToken).ConfigureAwait(false);
            var response = IpcJson.ParseResponse(frame);
            if (response.RequestId != request.RequestId && !(response.RequestId == "" && !response.Ok))
                throw new ServiceClientException("protocol_violation", "response does not match the request");
            return response;
        }
        finally { exchange.Release(); }
    }

    public ValueTask DisposeAsync() => pipe.DisposeAsync();
}

/// <summary>Raises UAC for one fixed executable and reports a refusal as such, not as a failure.</summary>
public static class Elevation
{
    public enum Outcome { Completed, Cancelled, CouldNotStart }

    public sealed record Result(Outcome Outcome, int ExitCode, string Detail);

    public static async Task<Result> RunAsync(string executable, IReadOnlyList<string> arguments, CancellationToken cancellationToken = default)
    {
        var info = new ProcessStartInfo
        {
            FileName = executable,
            Arguments = string.Join(' ', arguments.Select(Quote)),
            UseShellExecute = true,
            Verb = "runas",
            WindowStyle = ProcessWindowStyle.Hidden,
            WorkingDirectory = Path.GetDirectoryName(executable) ?? "",
        };
        try
        {
            using var process = Process.Start(info) ?? throw new InvalidOperationException("no process was started");
            await process.WaitForExitAsync(cancellationToken).ConfigureAwait(false);
            return new Result(Outcome.Completed, process.ExitCode, "finished");
        }
        catch (Win32Exception error) when (error.NativeErrorCode == Native.ErrorCancelled)
        {
            return new Result(Outcome.Cancelled, ServiceConstants.ExitCodes.ElevationCancelled, "the administrator prompt was declined");
        }
        catch (Exception error) when (error is Win32Exception or InvalidOperationException)
        {
            return new Result(Outcome.CouldNotStart, -1, error.Message);
        }
    }

    private static string Quote(string argument) =>
        argument.Length > 0 && argument.All(c => char.IsAsciiLetterOrDigit(c) || c is '-' or '_' or '.') ? argument : "\"" + argument.Replace("\"", "\\\"") + "\"";
}

using System.Text.Json;
using AsterLink.Shared.Client;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;

namespace AsterLink.Platform;

/// <summary>
/// The GUI's one channel to the Core: the verified service pipe. The first thing sent is hello (which makes the
/// service start the Core and check versions); a failed hello is a service error, not a login problem. If the pipe
/// breaks the service has already stopped the Core, so the transport reports a lost worker and the next
/// <see cref="StartAsync"/> opens a fresh connection that knows nothing of the old session.
/// </summary>
public sealed class ServiceCoreTransport(InstallLayout layout, string appVersion) : ICoreTransport
{
    private readonly SemaphoreSlim lifecycle = new(1, 1);
    private ServiceConnection? connection;
    private int inFlight;

    public async Task StartAsync(CancellationToken cancellationToken = default)
    {
        await lifecycle.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (connection is not null) return;
            ServiceConnection fresh;
            try { fresh = await ServiceConnection.ConnectAsync(layout, TimeSpan.FromSeconds(10), cancellationToken).ConfigureAwait(false); }
            catch (ServiceClientException error) { throw new CoreUnavailableException(error.Code, error.Message); }

            try
            {
                var hello = await fresh.SendAsync(new IpcRequest
                {
                    RequestId = NewId(),
                    Command = IpcCommands.Hello,
                    Payload = JsonSerializer.SerializeToElement(new { app_version = appVersion }),
                }, cancellationToken).ConfigureAwait(false);
                if (!hello.Ok) throw new IpcCommandException(hello.Error?.Code ?? "unknown", hello.Error?.Message ?? "");
            }
            catch
            {
                await fresh.DisposeAsync().ConfigureAwait(false);
                throw;
            }
            connection = fresh;
        }
        catch (Exception error) when (error is IOException or JsonException or ServiceClientException)
        {
            throw new CoreUnavailableException(ServiceErrors.WorkerExited, error.Message);
        }
        finally { lifecycle.Release(); }
    }

    public async Task<IpcResponse> SendAsync(string command, ulong generation, JsonElement? payload, CancellationToken cancellationToken = default)
    {
        await StartAsync(cancellationToken).ConfigureAwait(false);
        var current = connection ?? throw new CoreUnavailableException(ServiceErrors.WorkerExited, "not connected");
        Interlocked.Increment(ref inFlight);
        try
        {
            return await current.SendAsync(new IpcRequest { RequestId = NewId(), Generation = generation, Command = command, Payload = payload }, cancellationToken).ConfigureAwait(false);
        }
        catch (Exception error) when (error is IOException or ObjectDisposedException or IpcFrameException or JsonException or ServiceClientException)
        {
            // The pipe is no longer trustworthy (service died, Core lost, or a protocol violation): drop it.
            await DropAsync(current).ConfigureAwait(false);
            throw new CoreUnavailableException(ServiceErrors.WorkerExited, error.Message);
        }
        finally { Interlocked.Decrement(ref inFlight); }
    }

    public async Task StopAsync()
    {
        ServiceConnection? current;
        await lifecycle.WaitAsync().ConfigureAwait(false);
        try { current = connection; connection = null; }
        finally { lifecycle.Release(); }
        if (current is null) return;

        try
        {
            // With a command still in flight the pipe is busy; closing it is what ends that session.
            if (Volatile.Read(ref inFlight) == 0)
            {
                using var limit = new CancellationTokenSource(TimeSpan.FromSeconds(15));
                await current.SendAsync(new IpcRequest { RequestId = NewId(), Command = IpcCommands.Shutdown }, limit.Token).ConfigureAwait(false);
            }
        }
        catch (Exception error) when (error is IOException or ObjectDisposedException or OperationCanceledException or IpcFrameException or JsonException or ServiceClientException) { }
        finally { await current.DisposeAsync().ConfigureAwait(false); }
    }

    private async Task DropAsync(ServiceConnection dead)
    {
        await lifecycle.WaitAsync().ConfigureAwait(false);
        try { if (ReferenceEquals(connection, dead)) connection = null; }
        finally { lifecycle.Release(); }
        try { await dead.DisposeAsync().ConfigureAwait(false); } catch (Exception error) when (error is IOException or ObjectDisposedException) { }
    }

    private static string NewId() => Guid.NewGuid().ToString("N");
}

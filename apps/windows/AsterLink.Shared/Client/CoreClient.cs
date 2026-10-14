using System.Text.Json;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Snapshot;

namespace AsterLink.Shared.Client;

/// <summary>
/// Carries one command to the Core and returns the raw response. Production runs it over the
/// verified service pipe; tests substitute a scripted transport. The transport never interprets
/// a business outcome: it only moves frames.
/// </summary>
public interface ICoreTransport
{
    /// <summary>Opens the connection (and, for the service pipe, runs hello). Idempotent.</summary>
    Task StartAsync(CancellationToken cancellationToken = default);

    Task<IpcResponse> SendAsync(string command, ulong generation, JsonElement? payload, CancellationToken cancellationToken = default);

    /// <summary>Closes the connection; the service then stops the Core, which settles and revokes best-effort.</summary>
    Task StopAsync();
}

/// <summary>The Core or the service could not be reached at all (as opposed to a business refusal).</summary>
public sealed class CoreUnavailableException(string code, string message) : Exception(message)
{
    public string Code { get; } = code;
}

/// <summary>
/// Typed commands over an <see cref="ICoreTransport"/> (mirror of the Swift CoreClient). The password
/// appears only in the one-shot login payload built here; nothing keeps it afterwards.
/// </summary>
public sealed class CoreClient(ICoreTransport transport, string apiBase, string channel, string appVersion)
{
    public Task StartAsync(CancellationToken cancellationToken = default) => transport.StartAsync(cancellationToken);

    public Task StopAsync() => transport.StopAsync();

    public Task<SnapshotReply> LoginAsync(string email, string password, string deviceId, CancellationToken cancellationToken = default) =>
        ActionAsync(IpcCommands.Login, 0, new Dictionary<string, string>
        {
            ["email"] = email,
            ["password"] = password,
            ["api_base"] = apiBase,
            ["channel"] = channel,
            ["device_id"] = deviceId,
            ["app_version"] = appVersion,
        }, cancellationToken);

    public async Task<CoreSnapshot> ReadSnapshotAsync(CancellationToken cancellationToken = default) =>
        IpcJson.ParseSnapshotReply(await transport.SendAsync(IpcCommands.ReadSnapshot, 0, null, cancellationToken).ConfigureAwait(false)).Snapshot;

    public Task<SnapshotReply> RefreshCatalogAsync(ulong generation, CancellationToken cancellationToken = default) =>
        ActionAsync(IpcCommands.RefreshCatalog, generation, null, cancellationToken);

    public Task<SnapshotReply> SelectNodeAsync(ulong generation, string nodeId, string catalogRevision, CancellationToken cancellationToken = default) =>
        ActionAsync(IpcCommands.SelectNode, generation, new Dictionary<string, string> { ["node_id"] = nodeId, ["catalog_revision"] = catalogRevision }, cancellationToken);

    public Task<SnapshotReply> ConnectAsync(ulong generation, CancellationToken cancellationToken = default) =>
        ActionAsync(IpcCommands.Connect, generation, null, cancellationToken);

    public async Task<CoreSnapshot> DisconnectAsync(CancellationToken cancellationToken = default) =>
        IpcJson.ParseSnapshotReply(await transport.SendAsync(IpcCommands.Disconnect, 0, null, cancellationToken).ConfigureAwait(false)).Snapshot;

    public async Task<CoreSnapshot> LogoutAsync(CancellationToken cancellationToken = default) =>
        IpcJson.ParseSnapshotReply(await transport.SendAsync(IpcCommands.Logout, 0, null, cancellationToken).ConfigureAwait(false)).Snapshot;

    /// <summary>
    /// A refused command (stale generation, malformed payload, service error) throws
    /// <see cref="IpcCommandException"/>; a business failure comes back inside the reply as ActionError.
    /// </summary>
    private async Task<SnapshotReply> ActionAsync(string command, ulong generation, Dictionary<string, string>? payload, CancellationToken cancellationToken)
    {
        JsonElement? body = payload is null ? null : JsonSerializer.SerializeToElement(payload);
        var response = await transport.SendAsync(command, generation, body, cancellationToken).ConfigureAwait(false);
        return IpcJson.ParseSnapshotReply(response);
    }
}

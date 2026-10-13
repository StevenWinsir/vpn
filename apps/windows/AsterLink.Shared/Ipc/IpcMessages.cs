using System.Text.Json;
using AsterLink.Shared.Snapshot;

namespace AsterLink.Shared.Ipc;

public static class IpcProtocol
{
    public const int Version = 1;
}

public static class IpcCommands
{
    public const string Hello = "hello";
    public const string Login = "login";
    public const string ReadSnapshot = "read_snapshot";
    public const string RefreshCatalog = "refresh_catalog";
    public const string SelectNode = "select_node";
    public const string Connect = "connect";
    public const string Disconnect = "disconnect";
    public const string Logout = "logout";
    public const string Shutdown = "shutdown";
}

public sealed class IpcRequest
{
    public string RequestId { get; init; } = "";
    public int ProtocolVersion { get; init; } = IpcProtocol.Version;
    public ulong Generation { get; init; }
    public string Command { get; init; } = "";
    public JsonElement? Payload { get; init; }
}

public sealed class IpcError
{
    public string Code { get; init; } = "";
    public string Message { get; init; } = "";
}

public sealed class IpcResponse
{
    public string RequestId { get; init; } = "";
    public int ProtocolVersion { get; init; }
    public ulong Generation { get; init; }
    public bool Ok { get; init; }
    public JsonElement? Result { get; init; }
    public IpcError? Error { get; init; }
}

/// <summary>
/// What a state-changing command really produced. The Core answers a failed action with
/// <c>ok=true</c>, the new snapshot and an <c>action_error</c>; "ok" alone never means the
/// action succeeded, so the UI must look at <see cref="ActionError"/> as well.
/// </summary>
public sealed record SnapshotReply(CoreSnapshot Snapshot, ErrorView? ActionError)
{
    public bool Succeeded => ActionError is null;
}

public static class IpcJson
{
    public static byte[] SerializeRequest(IpcRequest request) =>
        JsonSerializer.SerializeToUtf8Bytes(request, SnapshotJson.Options);

    public static IpcResponse ParseResponse(ReadOnlySpan<byte> frame) =>
        JsonSerializer.Deserialize<IpcResponse>(frame, SnapshotJson.Options)
        ?? throw new JsonException("response is null");

    /// <summary>Transport failure (ok=false) throws; an action failure is returned inside the reply.</summary>
    public static SnapshotReply ParseSnapshotReply(IpcResponse response)
    {
        if (!response.Ok)
            throw new IpcCommandException(response.Error?.Code ?? "unknown", response.Error?.Message ?? "");
        if (response.Result is not { } result)
            throw new JsonException("snapshot result missing");

        var snapshot = SnapshotJson.ParseSnapshot(result);
        ErrorView? actionError = null;
        if (result.TryGetProperty("action_error", out var raw) && raw.ValueKind == JsonValueKind.Object)
            actionError = raw.Deserialize<ErrorView>(SnapshotJson.Options);
        return new SnapshotReply(snapshot, actionError);
    }
}

public sealed class IpcCommandException(string code, string message) : Exception($"{code}: {message}")
{
    public string Code { get; } = code;
}

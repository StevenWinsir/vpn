using System.Text.Json;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Snapshot;

namespace AsterLink.Shared.Service;

/// <summary>Stable error codes the service itself answers with (the Core has its own).</summary>
public static class ServiceErrors
{
    public const string InvalidRequest = "invalid_request";
    public const string UnknownCommand = "unknown_command";
    public const string PayloadTooLarge = "payload_too_large";
    public const string ProtocolUnsupported = "protocol_unsupported";
    public const string PeerRejected = "peer_rejected";
    public const string OwnerBusy = "owner_busy";
    public const string VersionMismatch = "version_mismatch";
    public const string IntegrityFailed = "integrity_failed";
    public const string RecoveryBlocked = "recovery_blocked";
    public const string LoginTargetMismatch = "login_target_mismatch";
    public const string WorkerUnavailable = "worker_unavailable";
    public const string WorkerExited = "worker_exited";
    public const string WorkerUnresponsive = "worker_unresponsive";
    public const string ServiceNotInstalled = "service_not_installed";
    public const string ServiceNotRunning = "service_not_running";
    public const string ServerRejected = "server_rejected";
}

public sealed class ServiceHelloResult
{
    public string Component { get; init; } = ServiceConstants.ServiceComponent;
    public string ServiceVersion { get; init; } = "";
    public string? CoreVersion { get; init; }
    public string? MihomoRevision { get; init; }
    public int ProtocolVersion { get; init; } = IpcProtocol.Version;
    public int MinProtocolVersion { get; init; } = IpcProtocol.Version;
    public int MaxProtocolVersion { get; init; } = IpcProtocol.Version;
    public string Channel { get; init; } = "";
}

/// <summary>Builds response frames in the same wire shape the Core uses (ipc-v1).</summary>
public static class ServiceReplies
{
    public static byte[] Error(string requestId, ulong generation, string code, string message) =>
        JsonSerializer.SerializeToUtf8Bytes(new IpcResponse
        {
            RequestId = requestId,
            ProtocolVersion = IpcProtocol.Version,
            Generation = generation,
            Ok = false,
            Error = new IpcError { Code = code, Message = message },
        }, SnapshotJson.Options);

    public static byte[] Ok(string requestId, ulong generation, object result) =>
        JsonSerializer.SerializeToUtf8Bytes(new IpcResponse
        {
            RequestId = requestId,
            ProtocolVersion = IpcProtocol.Version,
            Generation = generation,
            Ok = true,
            Result = JsonSerializer.SerializeToElement(result, SnapshotJson.Options),
        }, SnapshotJson.Options);
}

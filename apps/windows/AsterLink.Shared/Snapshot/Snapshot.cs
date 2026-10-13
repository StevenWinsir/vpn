namespace AsterLink.Shared.Snapshot;

// Mirrors shared/contracts/snapshot-v1.json (Go: native/core/internal/managed/snapshot.go).
// The Snapshot is redacted by construction: there is no field that could carry a token,
// password, YAML, server address, port or node credential. Every integer is a 64-bit
// long (never double) so values above 2^53 survive the round trip.

public sealed class UserView
{
    public string Id { get; init; } = "";
    public string Email { get; init; } = "";
    public string Name { get; init; } = "";
    public string Role { get; init; } = "";
}

public sealed class NodeView
{
    public string Id { get; init; } = "";
    public string Name { get; init; } = "";
    public string Region { get; init; } = "";
    public string LineType { get; init; } = "";
    public long RatePermille { get; init; }

    /// <summary>"node_reported" when billed from the node's own counters; empty otherwise.</summary>
    public string MeteringSource { get; init; } = "";
}

public sealed class EntitlementView
{
    public string PlanName { get; init; } = "";
    public DateTimeOffset? SubscriptionExpiresAt { get; init; }
    public DateTimeOffset? AuthorizationExpiresAt { get; init; }
    public long LeaseRemainingSeconds { get; init; }
    public bool AccountAuthorized { get; init; }
    public bool CanConnect { get; init; }
    public string Reason { get; init; } = "";
    public string ReasonMessage { get; init; } = "";
    public long TotalBytes { get; init; }
    public long RemainingBytes { get; init; }

    /// <summary>1 unit = 1/1000 byte (a 1000 permille line charges 1000 units per byte).</summary>
    public long UsedUnits { get; init; }
}

public sealed class MeterSample
{
    public long Upload { get; init; }
    public long Download { get; init; }
}

public sealed class MeteringView
{
    public string Source { get; init; } = "";
    public long UploadBytes { get; init; }
    public long DownloadBytes { get; init; }
    public long LastSequence { get; init; }
    public long UploadBytesPerSecond { get; init; }
    public long DownloadBytesPerSecond { get; init; }
    public long ConfirmedUploadBytes { get; init; }
    public long ConfirmedDownloadBytes { get; init; }
    public long ConfirmedRemainingBytes { get; init; }
    public long EstimatedRemainingBytes { get; init; }
    public long RatePermille { get; init; }
    public bool Pending { get; init; }
    public IReadOnlyList<MeterSample> History { get; init; } = [];
}

public sealed class ErrorView
{
    public string Code { get; init; } = "";
    public string Message { get; init; } = "";
}

/// <summary>The complete UI state returned by the Core. The GUI renders only this.</summary>
public sealed class CoreSnapshot
{
    public long Generation { get; init; }
    public string State { get; init; } = "";
    public UserView? User { get; init; }
    public EntitlementView? Entitlement { get; init; }
    public IReadOnlyList<NodeView> Nodes { get; init; } = [];
    public string SelectedNodeId { get; init; } = "";
    public string CatalogRevision { get; init; } = "";
    public string ProfileVersion { get; init; } = "";
    public bool ConfigReady { get; init; }
    public string TunnelHealth { get; init; } = "";
    public MeteringView? Metering { get; init; }
    public ErrorView? LastError { get; init; }
    public string CoreVersion { get; init; } = "";
}

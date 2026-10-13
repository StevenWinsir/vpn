using System.Text.Json;
using System.Text.Json.Serialization;

namespace AsterLink.Shared.Service;

/// <summary>
/// The recovery record the service persists in %ProgramData%\AsterLink before a Core is
/// allowed to run (it is written while the new process is still suspended). It names exactly
/// the one process this product started, never a global resource, and holds no credentials.
/// </summary>
public sealed class SessionMarker
{
    public const int Format = 1;

    private static readonly JsonSerializerOptions Options = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Disallow,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    };

    public int FormatVersion { get; init; } = Format;
    public int ServiceProcessId { get; init; }
    public int CoreProcessId { get; init; }
    /// <summary>FILETIME of the Core's creation: PID plus start time identifies one process for good.</summary>
    public long CoreStartFileTime { get; init; }
    public string CorePath { get; init; } = "";
    public string OwnerSid { get; init; } = "";
    public DateTimeOffset StartedAt { get; init; }

    public byte[] ToJson() => JsonSerializer.SerializeToUtf8Bytes(this, Options);

    /// <summary>Returns null for anything that is not a well-formed marker (the caller treats that as corrupt).</summary>
    public static SessionMarker? TryParse(ReadOnlySpan<byte> json)
    {
        try
        {
            var marker = JsonSerializer.Deserialize<SessionMarker>(json, Options);
            return marker is { FormatVersion: Format, CoreProcessId: > 0, CoreStartFileTime: > 0 }
                && WinPath.IsSafeAbsolute(marker.CorePath) ? marker : null;
        }
        catch (JsonException) { return null; }
    }
}

public enum RecoveryAction
{
    /// <summary>No marker: nothing was running.</summary>
    Nothing,
    /// <summary>The recorded process is gone (or is a different process now): just clear the record.</summary>
    ClearOnly,
    /// <summary>The recorded Core still runs: terminate that process, then clear the record.</summary>
    KillThenClear,
    /// <summary>The record is unreadable: do not guess, block connecting until the repair verb archives it.</summary>
    Block,
}

public sealed record RunningProcess(long StartFileTime, string? ImagePath);

public static class RecoveryPolicy
{
    /// <param name="markerFilePresent">A file exists at the marker path.</param>
    /// <param name="marker">Parsed marker, null if it failed to parse.</param>
    /// <param name="process">What currently runs under the recorded PID, null if nothing does.</param>
    public static RecoveryAction Decide(bool markerFilePresent, SessionMarker? marker, RunningProcess? process)
    {
        if (!markerFilePresent) return RecoveryAction.Nothing;
        if (marker is null) return RecoveryAction.Block;
        if (process is null) return RecoveryAction.ClearOnly;
        // The same PID can belong to an unrelated program later. Only an exact start time and
        // image match is our orphan; anything else is not ours to kill.
        var ours = process.StartFileTime == marker.CoreStartFileTime
            && WinPath.Equal(process.ImagePath, marker.CorePath);
        return ours ? RecoveryAction.KillThenClear : RecoveryAction.ClearOnly;
    }
}

namespace AsterLink.Shared.Service;

/// <summary>
/// Facts the service measured about whoever connected to the pipe. They are read from the
/// operating system (pipe handle, process handle, token), never taken from the request.
/// </summary>
public sealed record PeerFacts(
    int ProcessId,
    int PipeSessionId,
    int ProcessSessionId,
    string? TokenUserSid,
    string? ImpersonatedUserSid,
    uint IntegrityRid,
    string? ImagePath,
    string? ImageSha256);

public enum PeerVerdict
{
    Allowed,
    UnknownIdentity,
    UserMismatch,
    ServiceAccount,
    SessionZero,
    SessionMismatch,
    LowIntegrity,
    ImagePath,
    ImageHash,
}

public sealed record PeerDecision(PeerVerdict Verdict, string Detail)
{
    public bool Allowed => Verdict == PeerVerdict.Allowed;
}

/// <summary>
/// Who may talk to the service: the installed GUI binary (path and hash from the manifest),
/// running as an ordinary interactive user. The decision is pure so every refusal reason is
/// unit-tested on any OS; collecting the facts is the Windows part.
/// </summary>
public static class PeerPolicy
{
    public const uint MediumIntegrity = 0x2000;

    /// <summary>Accounts that are never a desktop user: SYSTEM, LOCAL SERVICE, NETWORK SERVICE, ANONYMOUS, Everyone.</summary>
    private static readonly HashSet<string> NonInteractive = new(StringComparer.OrdinalIgnoreCase)
    {
        "S-1-5-18", "S-1-5-19", "S-1-5-20", "S-1-5-7", "S-1-1-0",
    };

    public static PeerDecision Evaluate(PeerFacts peer, string expectedGuiPath, string expectedGuiSha256)
    {
        if (string.IsNullOrEmpty(peer.TokenUserSid) || string.IsNullOrEmpty(peer.ImpersonatedUserSid))
            return Deny(PeerVerdict.UnknownIdentity, "caller identity could not be established");

        // The process the PID names must be the same principal the pipe connection really carries.
        // A reused PID or a client that opened the pipe and handed it to another process fails here.
        if (!string.Equals(peer.TokenUserSid, peer.ImpersonatedUserSid, StringComparison.OrdinalIgnoreCase))
            return Deny(PeerVerdict.UserMismatch, "process owner differs from the pipe client");

        if (NonInteractive.Contains(peer.TokenUserSid))
            return Deny(PeerVerdict.ServiceAccount, "service and anonymous accounts cannot use the desktop pipe");

        if (peer.PipeSessionId <= 0) return Deny(PeerVerdict.SessionZero, "session 0 clients are refused");
        if (peer.PipeSessionId != peer.ProcessSessionId) return Deny(PeerVerdict.SessionMismatch, "pipe and process sessions differ");

        if (peer.IntegrityRid < MediumIntegrity) return Deny(PeerVerdict.LowIntegrity, "low-integrity callers are refused");

        if (!WinPath.Equal(peer.ImagePath, expectedGuiPath))
            return Deny(PeerVerdict.ImagePath, "caller is not the installed GUI executable");
        if (!string.Equals(peer.ImageSha256, expectedGuiSha256, StringComparison.OrdinalIgnoreCase))
            return Deny(PeerVerdict.ImageHash, "caller executable does not match the manifest");

        return new PeerDecision(PeerVerdict.Allowed, "ok");
    }

    private static PeerDecision Deny(PeerVerdict verdict, string detail) => new(verdict, detail);
}

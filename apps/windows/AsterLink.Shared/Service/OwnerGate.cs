namespace AsterLink.Shared.Service;

/// <summary>A live GUI connection that owns (or owned) the single tunnel slot.</summary>
public interface IOwnerConnection
{
    /// <summary>Ends the connection and completes only after its worker is fully gone.</summary>
    Task CloseAsync();
}

public enum OwnerOutcome { Granted, Busy }

public sealed record OwnerClaim(OwnerOutcome Outcome, IOwnerConnection? Previous);

/// <summary>
/// At most one Windows user session owns one Core/TUN at a time (handoff section 4). A second
/// user is refused rather than stopping the first. The same user reconnecting (the GUI was
/// restarted or crashed) replaces the old connection; the caller must await the returned
/// previous connection's <see cref="IOwnerConnection.CloseAsync"/> before starting its own
/// worker, so two Cores can never run together.
/// </summary>
public sealed class OwnerGate
{
    private readonly object gate = new();
    private (string Sid, int Session, IOwnerConnection Connection)? current;

    public OwnerClaim TryClaim(string sid, int session, IOwnerConnection connection)
    {
        lock (gate)
        {
            if (current is { } owner)
            {
                var sameUser = string.Equals(owner.Sid, sid, StringComparison.OrdinalIgnoreCase) && owner.Session == session;
                if (!sameUser) return new OwnerClaim(OwnerOutcome.Busy, null);
                current = (sid, session, connection);
                return new OwnerClaim(OwnerOutcome.Granted, owner.Connection);
            }
            current = (sid, session, connection);
            return new OwnerClaim(OwnerOutcome.Granted, null);
        }
    }

    public void Release(IOwnerConnection connection)
    {
        lock (gate)
        {
            if (current is { } owner && ReferenceEquals(owner.Connection, connection)) current = null;
        }
    }

    public bool IsOwned
    {
        get { lock (gate) return current is not null; }
    }
}

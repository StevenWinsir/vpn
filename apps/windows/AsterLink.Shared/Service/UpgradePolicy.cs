namespace AsterLink.Shared.Service;

public enum UpgradeKind { Fresh, Reinstall, Upgrade, Downgrade }

/// <summary>
/// What installing a stage over whatever is installed means. A downgrade is refused unless the operator
/// asks for it, so an old installer downloaded by mistake cannot silently replace a newer product.
/// Versions are the strict <c>major.minor.patch</c> form (<see cref="BuildVersion.IsWellFormed"/>).
/// </summary>
public static class UpgradePolicy
{
    public static UpgradeKind Classify(string? installedVersion, string incomingVersion)
    {
        if (!BuildVersion.IsWellFormed(incomingVersion)) throw new ArgumentException("incoming version is not major.minor.patch", nameof(incomingVersion));
        // An installed copy whose version cannot be read is treated as a fresh install: the verb replaces it whole anyway.
        if (!BuildVersion.IsWellFormed(installedVersion)) return UpgradeKind.Fresh;
        var order = Compare(incomingVersion, installedVersion!);
        return order == 0 ? UpgradeKind.Reinstall : order > 0 ? UpgradeKind.Upgrade : UpgradeKind.Downgrade;
    }

    public static bool Allowed(UpgradeKind kind, bool allowDowngrade) => kind != UpgradeKind.Downgrade || allowDowngrade;

    public static int Compare(string left, string right)
    {
        var a = left.Split('.').Select(int.Parse).ToArray();
        var b = right.Split('.').Select(int.Parse).ToArray();
        for (var i = 0; i < 3; i++)
            if (a[i] != b[i]) return a[i].CompareTo(b[i]);
        return 0;
    }
}

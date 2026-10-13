namespace AsterLink.Shared.Service;

/// <summary>One access-control entry, reduced to what the policy needs.</summary>
public sealed record AceFact(string Sid, bool Allow, int Rights, bool InheritOnly);

public sealed record AclVerdict(bool Safe, string Detail);

/// <summary>
/// "Protected" means only SYSTEM, Administrators and TrustedInstaller can change the
/// directory or what is in it. Anything else holding write, delete, permission or ownership
/// rights lets a standard user swap the Core or a DLL that the service later runs as SYSTEM.
/// Pure and tested on any OS; the Windows layer extracts the entries.
/// </summary>
public static class AclPolicy
{
    // FileSystemRights bits that allow changing content, names, attributes or the ACL itself.
    public const int WriteData = 0x2;            // also AddFile
    public const int AppendData = 0x4;           // also AddSubdirectory
    public const int WriteExtendedAttributes = 0x10;
    public const int DeleteSubdirectoriesAndFiles = 0x40;
    public const int WriteAttributes = 0x100;
    public const int Delete = 0x10000;
    public const int ChangePermissions = 0x40000;
    public const int TakeOwnership = 0x80000;
    public const int GenericAll = 0x10000000;
    public const int GenericWrite = 0x40000000;

    public const int Dangerous = WriteData | AppendData | WriteExtendedAttributes | DeleteSubdirectoriesAndFiles
        | WriteAttributes | Delete | ChangePermissions | TakeOwnership | GenericAll | GenericWrite;

    private static readonly HashSet<string> Trusted = new(StringComparer.OrdinalIgnoreCase)
    {
        "S-1-5-18",      // SYSTEM
        "S-1-5-32-544",  // Administrators
        "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464", // NT SERVICE\TrustedInstaller
        "S-1-3-0",       // CREATOR OWNER: only applies to what an owner creates, owner is checked separately
    };

    public static AclVerdict Evaluate(string? ownerSid, IEnumerable<AceFact> entries)
    {
        if (ownerSid is null || !Trusted.Contains(ownerSid))
            return new(false, $"owner {ownerSid ?? "unknown"} is not SYSTEM, Administrators or TrustedInstaller");
        foreach (var ace in entries)
        {
            if (!ace.Allow || ace.InheritOnly && ace.Sid == "S-1-3-0") continue;
            if ((ace.Rights & Dangerous) != 0 && !Trusted.Contains(ace.Sid))
                return new(false, $"{ace.Sid} may modify this location (rights 0x{ace.Rights:X})");
        }
        return new(true, "ok");
    }
}

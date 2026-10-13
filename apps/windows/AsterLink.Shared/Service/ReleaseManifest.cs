using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace AsterLink.Shared.Service;

public sealed class ManifestException(string message) : FormatException(message);

public sealed class ManifestFile
{
    public string Path { get; init; } = "";
    public string Sha256 { get; init; } = "";
    public long Size { get; init; }
    public string? Role { get; init; }
}

/// <summary>
/// The list of every file the product installs, with SHA-256. The service trusts the
/// manifest only because it lives in a directory that only SYSTEM and Administrators can
/// write; for the <c>internal</c> channel there is no signature (handoff round 3: "internal
/// signing is not a trusted release"). A <c>release</c> manifest is refused until Authenticode
/// verification exists (round 8), so the two can never be confused.
/// </summary>
public sealed class ReleaseManifest
{
    public const int Format = 1;
    public const string InternalChannel = "internal";
    public const string ReleaseChannel = "release";

    public static readonly IReadOnlyDictionary<string, string> RequiredRoles = new Dictionary<string, string>
    {
        ["gui"] = InstallLayout.AppExeRelative,
        ["service"] = InstallLayout.ServiceExeRelative,
        ["core"] = InstallLayout.CoreExeRelative,
        ["config"] = InstallLayout.ConfigRelative,
    };

    private static readonly JsonSerializerOptions Options = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Disallow,
        WriteIndented = true,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    };

    public int FormatVersion { get; init; }
    public string Version { get; init; } = "";
    public string Channel { get; init; } = "";
    public int ProtocolVersion { get; init; }
    public IReadOnlyList<ManifestFile> Files { get; init; } = [];

    public ManifestFile Role(string role) => Files.First(f => f.Role == role);

    public static ReleaseManifest Parse(ReadOnlySpan<byte> json)
    {
        ReleaseManifest? manifest;
        try { manifest = JsonSerializer.Deserialize<ReleaseManifest>(json, Options); }
        catch (JsonException error) { throw new ManifestException("manifest is not valid: " + error.Message); }
        if (manifest is null) throw new ManifestException("manifest is empty");
        manifest.Validate();
        return manifest;
    }

    public string ToJson() => JsonSerializer.Serialize(this, Options);

    public void Validate()
    {
        if (FormatVersion != Format) throw new ManifestException("unsupported manifest format");
        if (!BuildVersion.IsWellFormed(Version)) throw new ManifestException("invalid version");
        if (Channel is not (InternalChannel or ReleaseChannel)) throw new ManifestException("invalid channel");
        if (ProtocolVersion != Ipc.IpcProtocol.Version) throw new ManifestException("manifest protocol version differs from this build");
        if (Files.Count is 0 or > 5000) throw new ManifestException("invalid file count");

        var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (var file in Files)
        {
            if (!WinPath.IsSafeRelative(file.Path)) throw new ManifestException("unsafe path in manifest");
            if (!seen.Add(file.Path)) throw new ManifestException("duplicate path in manifest");
            if (file.Sha256.Length != 64 || !file.Sha256.All(c => c is (>= '0' and <= '9') or (>= 'a' and <= 'f')))
                throw new ManifestException("invalid sha256 in manifest");
            if (file.Size < 0) throw new ManifestException("invalid size in manifest");
            var top = file.Path.Split('/')[0];
            var allowed = file.Path == InstallLayout.ConfigRelative
                || InstallLayout.ManagedDirectories.Contains(top, StringComparer.Ordinal) && file.Path.Contains('/');
            if (!allowed) throw new ManifestException("manifest lists a file outside the managed directories");
        }

        foreach (var (role, expected) in RequiredRoles)
        {
            var holders = Files.Where(f => f.Role == role).ToList();
            if (holders.Count != 1 || !string.Equals(holders[0].Path, expected, StringComparison.Ordinal))
                throw new ManifestException($"role {role} must be exactly {expected}");
        }
        if (Files.Any(f => f.Role is not null && !RequiredRoles.ContainsKey(f.Role)))
            throw new ManifestException("unknown role in manifest");
    }

    public static string HashFile(string path)
    {
        using var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read);
        return HashStream(stream);
    }

    public static string HashStream(Stream stream) => Convert.ToHexStringLower(SHA256.HashData(stream));
}

public enum IntegrityProblemKind { Missing, HashMismatch, SizeMismatch, UnlistedFile, NotARegularFile }

public sealed record IntegrityProblem(IntegrityProblemKind Kind, string Path);

/// <summary>Walks an install root (or a stage tree) against its manifest.</summary>
public static class ManifestVerifier
{
    public static IReadOnlyList<IntegrityProblem> Verify(string root, ReleaseManifest manifest)
    {
        var problems = new List<IntegrityProblem>();
        var listed = new HashSet<string>(StringComparer.OrdinalIgnoreCase);

        foreach (var file in manifest.Files)
        {
            listed.Add(file.Path);
            var full = System.IO.Path.Combine(root, file.Path.Replace('/', System.IO.Path.DirectorySeparatorChar));
            var info = new FileInfo(full);
            if (!info.Exists) { problems.Add(new(IntegrityProblemKind.Missing, file.Path)); continue; }
            if (info.LinkTarget is not null || (info.Attributes & FileAttributes.ReparsePoint) != 0)
            { problems.Add(new(IntegrityProblemKind.NotARegularFile, file.Path)); continue; }
            if (info.Length != file.Size) { problems.Add(new(IntegrityProblemKind.SizeMismatch, file.Path)); continue; }
            if (!string.Equals(ManifestHash(full), file.Sha256, StringComparison.Ordinal))
                problems.Add(new(IntegrityProblemKind.HashMismatch, file.Path));
        }

        // A file nobody listed next to the product (a planted DLL, a second Core) is as
        // much an integrity failure as a modified one.
        foreach (var directory in InstallLayout.ManagedDirectories)
        {
            var full = System.IO.Path.Combine(root, directory);
            if (!Directory.Exists(full)) continue;
            foreach (var path in Directory.EnumerateFiles(full, "*", SearchOption.AllDirectories))
            {
                var relative = System.IO.Path.GetRelativePath(root, path).Replace(System.IO.Path.DirectorySeparatorChar, '/');
                if (!listed.Contains(relative)) problems.Add(new(IntegrityProblemKind.UnlistedFile, relative));
            }
        }
        return problems;
    }

    private static string ManifestHash(string path)
    {
        try { return ReleaseManifest.HashFile(path); }
        catch (IOException) { return ""; }
        catch (UnauthorizedAccessException) { return ""; }
    }
}

using AsterLink.Shared.Env;
using AsterLink.Shared.Service;

namespace AsterLink.Platform;

public sealed record TrustReport(
    ReleaseManifest? Manifest,
    ClientEnvironment? BuildConfig,
    IReadOnlyList<string> Problems)
{
    public bool Trusted => Problems.Count == 0 && Manifest is not null && BuildConfig is not null;
}

/// <summary>
/// "Is this install tree the one the installer wrote?" Combines the lexical, file-system, ACL and
/// manifest checks. The service runs it at start, at every GUI hello and before every Core launch.
/// </summary>
public static class InstallVerifier
{
    /// <summary>Full check: protected location, then every file's hash. Costs a read of the whole tree.</summary>
    public static TrustReport Verify(InstallLayout layout)
    {
        var problems = new List<string>();
        problems.AddRange(ProtectedFiles.Check(layout));
        var (manifest, config) = LoadMetadata(layout, problems);
        if (manifest is not null)
        {
            foreach (var problem in ManifestVerifier.Verify(layout.Root, manifest))
                problems.Add($"{problem.Kind}: {problem.Path}");
        }
        return new TrustReport(manifest, config, problems);
    }

    /// <summary>
    /// Parses the manifest and the build config without hashing the tree. Cheap enough to do before a
    /// caller has proven who it is; nothing read this way is trusted until <see cref="Verify"/> has run.
    /// </summary>
    public static TrustReport LoadMetadata(InstallLayout layout)
    {
        var problems = new List<string>();
        var (manifest, config) = LoadMetadata(layout, problems);
        return new TrustReport(manifest, config, problems);
    }

    private static (ReleaseManifest?, ClientEnvironment?) LoadMetadata(InstallLayout layout, List<string> problems)
    {
        ReleaseManifest? manifest = null;
        ClientEnvironment? config = null;
        try
        {
            manifest = ReleaseManifest.Parse(File.ReadAllBytes(layout.Manifest));
            if (manifest.Channel != ReleaseManifest.InternalChannel)
                problems.Add("only the internal channel can be verified by this build; release signatures are checked from round 8");
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or ManifestException)
        {
            problems.Add("manifest: " + error.Message);
        }
        try { config = BuildConfig.Parse(File.ReadAllBytes(layout.BuildConfig)); }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or ClientEnvironmentException)
        {
            problems.Add("build config: " + error.Message);
        }
        return (manifest, config);
    }

    /// <summary>The only install root a service will run from: %ProgramFiles%\AsterLink.</summary>
    public static InstallLayout ProgramFilesLayout() =>
        InstallLayout.ForProgramFiles(Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles));
}

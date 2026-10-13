namespace AsterLink.Shared.Service;

/// <summary>
/// The one on-disk layout, used identically by a staged build tree and by the installed
/// product, so the GUI can tell where it runs from its own location:
/// <code>
/// &lt;root&gt;\app\AsterLink.exe        GUI (+ self-contained runtime)
/// &lt;root&gt;\service\AsterLink.Service.exe
/// &lt;root&gt;\core\asterlink-core.exe  (Wintun is embedded, no DLL is ever searched for)
/// &lt;root&gt;\build-config.json        protected public build parameters
/// &lt;root&gt;\manifest.json            hashes of every file above
/// </code>
/// </summary>
public sealed record InstallLayout(string Root)
{
    public const string AppExeRelative = "app/AsterLink.exe";
    public const string ServiceExeRelative = "service/AsterLink.Service.exe";
    public const string CoreExeRelative = "core/asterlink-core.exe";
    public const string ConfigRelative = ServiceConstants.BuildConfigFile;

    /// <summary>Directories whose full content the manifest must account for.</summary>
    public static readonly string[] ManagedDirectories = ["app", "service", "core"];

    public string AppExe => Join(AppExeRelative);
    public string ServiceExe => Join(ServiceExeRelative);
    public string CoreExe => Join(CoreExeRelative);
    public string BuildConfig => Join(ConfigRelative);
    public string Manifest => Join(ServiceConstants.ManifestFile);

    public string Join(string relative) => Root.TrimEnd('\\') + "\\" + relative.Replace('/', '\\');

    /// <summary>The only root the service accepts: %ProgramFiles%\AsterLink.</summary>
    public static InstallLayout ForProgramFiles(string programFiles) =>
        new(programFiles.TrimEnd('\\') + "\\" + ServiceConstants.ProductDirectory);

    /// <summary>
    /// Derives the layout from the directory a component runs in (<c>&lt;root&gt;\app</c>,
    /// <c>&lt;root&gt;\service</c>). Null when the directory is not one of the managed ones.
    /// </summary>
    public static InstallLayout? FromComponentDirectory(string componentDirectory)
    {
        if (!WinPath.IsSafeAbsolute(componentDirectory)) return null;
        var trimmed = WinPath.Trim(componentDirectory);
        var cut = trimmed.LastIndexOf('\\');
        if (cut < 3) return null;
        var name = trimmed[(cut + 1)..];
        if (!ManagedDirectories.Contains(name, StringComparer.OrdinalIgnoreCase)) return null;
        return new InstallLayout(trimmed[..cut]);
    }

    public bool IsAt(string programFilesRoot) => WinPath.Equal(Root, programFilesRoot);
}

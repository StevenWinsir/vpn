namespace AsterLink.Shared.Service;

/// <summary>
/// Fixed identities of the privileged service. None of these can be changed at run time or
/// by a caller: a user-chosen pipe name or install path is exactly the kind of input the
/// service must never accept (handoff section 4.2).
/// </summary>
public static class ServiceConstants
{
    public const string ServiceName = "AsterLinkService";
    // Shown in the Windows Services list and Task Manager, so they are customer copy (handoff_app_standardization.md 7.1).
    public const string DisplayName = "AsterLink 后台服务";
    public const string Description = "为 AsterLink 提供安全连接所需的后台支持。";

    /// <summary>Appears as \\.\pipe\AsterLink.Service.v1. The suffix is the GUI-to-service pipe version.</summary>
    public const string PipeName = "AsterLink.Service.v1";

    public const string ProductDirectory = "AsterLink";
    public const string ManifestFile = "manifest.json";
    public const string BuildConfigFile = "build-config.json";
    public const string SessionMarkerFile = "session.json";

    /// <summary>Component names reported in <c>hello</c>.</summary>
    public const string ServiceComponent = "asterlink-service";

    /// <summary>How long the service waits for a Core to settle after the GUI is gone.</summary>
    public static readonly TimeSpan WorkerStopGrace = TimeSpan.FromSeconds(15);

    /// <summary>Longest the service waits for one Core reply before declaring the worker hung.</summary>
    public static readonly TimeSpan WorkerReplyTimeout = TimeSpan.FromSeconds(120);

    /// <summary>Exit codes of the service executable's command-line verbs.</summary>
    public static class ExitCodes
    {
        public const int Ok = 0;
        public const int Failed = 1;
        public const int Usage = 2;
        /// <summary>The verb needs an elevated token and this process has none.</summary>
        public const int NeedsElevation = 5;
        /// <summary>Installed files do not match the manifest; a repair cannot trust them.</summary>
        public const int IntegrityFailed = 6;
        /// <summary>The stage is older than what is installed and <c>--allow-downgrade</c> was not given.</summary>
        public const int DowngradeRefused = 7;
        /// <summary>The user declined the UAC prompt (GUI side).</summary>
        public const int ElevationCancelled = 1223;
    }
}

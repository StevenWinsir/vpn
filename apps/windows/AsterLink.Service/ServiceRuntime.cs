using AsterLink.Platform;
using AsterLink.Shared.Service;
using Microsoft.Extensions.Logging;

namespace AsterLink.Service;

/// <summary>State shared by every GUI connection: where the product is installed, whether startup recovery allows connecting, who owns the tunnel.</summary>
public sealed class ServiceRuntime(InstallLayout layout, RecoveryStore store, RecoveryOutcome recovery, ILogger<ServiceRuntime> logger)
{
    public InstallLayout Layout { get; } = layout;
    public RecoveryStore Store { get; } = store;
    public OwnerGate Owners { get; } = new();
    public ILogger Logger { get; } = logger;

    /// <summary>True when startup recovery could not explain what an earlier run left behind.</summary>
    public bool RecoveryBlocked { get; } = recovery.Blocked;
    public string RecoveryDetail { get; } = recovery.Detail;

    /// <summary>
    /// Builds the runtime for a service process. The service only runs from the protected default
    /// root: started from anywhere else (a downloaded copy, a build folder) it refuses to serve.
    /// </summary>
    public static ServiceRuntime? Create(ILoggerFactory loggers, out string? refusal)
    {
        var logger = loggers.CreateLogger<ServiceRuntime>();
        var own = InstallLayout.FromComponentDirectory(AppContext.BaseDirectory);
        var expected = InstallVerifier.ProgramFilesLayout();
        if (own is null || !own.IsAt(expected.Root))
        {
            refusal = "the service runs only from " + expected.Root;
            logger.LogError("{Reason}", refusal);
            return null;
        }
        refusal = null;

        var store = RecoveryStore.Default();
        var outcome = StartupRecovery.Run(store);
        logger.LogInformation("Startup recovery: {Action} ({Detail})", outcome.Action, outcome.Detail);
        return new ServiceRuntime(expected, store, outcome, logger);
    }
}

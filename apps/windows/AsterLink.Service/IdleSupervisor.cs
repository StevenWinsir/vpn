using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;

namespace AsterLink.Service;

public static class ServiceIdentity
{
    public const string Name = "AsterLinkService";
}

/// <summary>
/// Placeholder for the round-3 WorkerSupervisor. It deliberately does nothing but wait:
/// until the authenticated pipe, protected Core path and Job Object exist, the service must
/// not start a proxy or touch routes, and nothing here may report a tunnel as up.
/// </summary>
public sealed class IdleSupervisor(ILogger<IdleSupervisor> logger) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        logger.LogInformation("AsterLink service skeleton started; no tunnel capability in this build");
        try { await Task.Delay(Timeout.Infinite, stoppingToken); }
        catch (OperationCanceledException) { }
        logger.LogInformation("AsterLink service skeleton stopping");
    }
}

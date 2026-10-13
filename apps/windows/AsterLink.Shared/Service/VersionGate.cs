namespace AsterLink.Shared.Service;

public sealed record VersionVerdict(bool Compatible, string Detail);

/// <summary>
/// GUI, service and Core are installed together and must be the same release; the installed
/// manifest names the version all three have to report. A mixed set is a repair case, never
/// something to connect "as best as possible" (handoff section 4.2).
/// </summary>
public static class VersionGate
{
    public static VersionVerdict Check(string manifestVersion, string serviceVersion, string? guiVersion, string? coreVersion)
    {
        if (serviceVersion != manifestVersion)
            return new(false, $"service {serviceVersion} differs from the installed manifest {manifestVersion}");
        if (guiVersion is not null && guiVersion != manifestVersion)
            return new(false, $"GUI {guiVersion} differs from the installed manifest {manifestVersion}");
        if (coreVersion is not null && coreVersion != manifestVersion)
            return new(false, $"Core {coreVersion} differs from the installed manifest {manifestVersion}");
        return new(true, "ok");
    }
}

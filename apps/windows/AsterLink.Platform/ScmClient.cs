using System.ComponentModel;
using System.Runtime.InteropServices;
using AsterLink.Shared.Service;

namespace AsterLink.Platform;

public sealed record ScmInfo(ScmState State, int ProcessId, string? BinaryPath, string? StartName, uint StartType);

/// <summary>Read-only Service Control Manager queries any user may make, plus the start request.</summary>
public static class ScmClient
{
    private const uint Stopped = 1, StartPending = 2, StopPending = 3, Running = 4;

    public static ScmInfo Query(string serviceName = ServiceConstants.ServiceName)
    {
        using var manager = Native.OpenSCManagerW(null, null, Native.ScManagerConnect);
        if (manager.IsInvalid) throw new Win32Exception(Marshal.GetLastWin32Error(), "OpenSCManager");

        using var service = Native.OpenServiceW(manager, serviceName, Native.ServiceQueryConfig | Native.ServiceQueryStatus);
        if (service.IsInvalid)
        {
            var error = Marshal.GetLastWin32Error();
            if (error == Native.ErrorServiceDoesNotExist) return new ScmInfo(ScmState.NotInstalled, 0, null, null, 0);
            throw new Win32Exception(error, "OpenService");
        }

        if (!Native.QueryServiceStatusEx(service, Native.ScStatusProcessInfo, out var status, Marshal.SizeOf<Native.ServiceStatusProcess>(), out _))
            throw new Win32Exception(Marshal.GetLastWin32Error(), "QueryServiceStatusEx");

        string? path = null, account = null;
        uint startType = 0;
        _ = Native.QueryServiceConfigW(service, IntPtr.Zero, 0, out var needed);
        if (needed > 0)
        {
            var buffer = Marshal.AllocHGlobal(needed);
            try
            {
                if (Native.QueryServiceConfigW(service, buffer, needed, out _))
                {
                    var config = Marshal.PtrToStructure<Native.QueryServiceConfig>(buffer);
                    path = Marshal.PtrToStringUni(config.BinaryPathName);
                    account = Marshal.PtrToStringUni(config.ServiceStartName);
                    startType = config.StartType;
                }
            }
            finally { Marshal.FreeHGlobal(buffer); }
        }

        var state = status.CurrentState switch
        {
            Stopped => ScmState.Stopped,
            StartPending => ScmState.StartPending,
            StopPending => ScmState.StopPending,
            Running => ScmState.Running,
            _ => ScmState.Other,
        };
        return new ScmInfo(state, (int)status.ProcessId, path, account, startType);
    }

    /// <summary>Starts the service (needs an elevated token). Already running counts as started.</summary>
    public static void Start(string serviceName = ServiceConstants.ServiceName)
    {
        using var manager = Native.OpenSCManagerW(null, null, Native.ScManagerConnect);
        if (manager.IsInvalid) throw new Win32Exception(Marshal.GetLastWin32Error(), "OpenSCManager");
        using var service = Native.OpenServiceW(manager, serviceName, Native.ServiceStart | Native.ServiceQueryStatus);
        if (service.IsInvalid) throw new Win32Exception(Marshal.GetLastWin32Error(), "OpenService");
        if (!Native.StartServiceW(service, 0, IntPtr.Zero))
        {
            var error = Marshal.GetLastWin32Error();
            if (error != Native.ErrorServiceAlreadyRunning) throw new Win32Exception(error, "StartService");
        }
    }

    public static bool BinaryPathIs(ScmInfo info, string expectedExe) => ServerPolicy.BinaryPathIs(info.BinaryPath, expectedExe);
}

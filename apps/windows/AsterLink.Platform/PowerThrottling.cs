using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;

namespace AsterLink.Platform;

/// <summary>
/// Keeps the Core out of Windows' execution-speed throttling (handoff_fix.md F4, D8). Since
/// Windows 11 22H2 a process with no window of its own, such as a child of a service, gets the
/// Utility QoS level: on battery it runs at the most efficient CPU frequency and is scheduled
/// on efficiency cores. The Core is the whole VPN data path, so the service opts it out
/// explicitly, which puts it at High QoS. The priority class stays Normal: nothing here lets the
/// Core preempt the user's foreground programs.
/// </summary>
public static class PowerThrottling
{
    /// <summary>
    /// Opts <paramref name="process"/> out of execution-speed throttling. Never throws: returns 0 on
    /// success, otherwise the Win32 error (for example on a Windows build that does not know the
    /// information class), and the caller only logs it, because a Core that runs throttled still
    /// works, while one that does not start does not.
    /// </summary>
    public static int OptOut(IntPtr process)
    {
        var state = new Native.ProcessPowerThrottlingState
        {
            Version = Native.ProcessPowerThrottlingCurrentVersion,
            // Control bit set, state bit clear: "manage execution speed, and do not throttle it".
            ControlMask = Native.ProcessPowerThrottlingExecutionSpeed,
            StateMask = 0,
        };
        return Native.SetProcessInformation(process, Native.ProcessPowerThrottling, ref state, Marshal.SizeOf<Native.ProcessPowerThrottlingState>())
            ? 0
            : Marshal.GetLastWin32Error();
    }

    /// <summary>The process's execution-speed throttling policy as set on it, or null if unreadable.</summary>
    public static ThrottlingPolicy? Read(SafeProcessHandle process)
    {
        var state = new Native.ProcessPowerThrottlingState { Version = Native.ProcessPowerThrottlingCurrentVersion };
        if (!Native.GetProcessInformation(process, Native.ProcessPowerThrottling, ref state, Marshal.SizeOf<Native.ProcessPowerThrottlingState>()))
            return null;
        return new ThrottlingPolicy(
            Managed: (state.ControlMask & Native.ProcessPowerThrottlingExecutionSpeed) != 0,
            Throttled: (state.StateMask & Native.ProcessPowerThrottlingExecutionSpeed) != 0);
    }
}

/// <summary>
/// <paramref name="Managed"/>: the process sets its own execution-speed policy instead of leaving it
/// to the system's heuristics. <paramref name="Throttled"/>: that policy is EcoQoS. The Core must be
/// managed and not throttled.
/// </summary>
public sealed record ThrottlingPolicy(bool Managed, bool Throttled);

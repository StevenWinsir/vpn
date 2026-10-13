using System.ComponentModel;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;

namespace AsterLink.Platform;

public sealed record ProcessFacts(string? ImagePath, string? UserSid, uint IntegrityRid, int SessionId, long StartFileTime);

/// <summary>Reads facts about a process from the operating system by PID, holding a handle while it does.</summary>
public static class ProcessIdentity
{
    /// <summary>Null when the process is gone or cannot be opened (treated as "not identifiable" by callers).</summary>
    public static ProcessFacts? Read(int processId)
    {
        using var process = Native.OpenProcess(Native.ProcessQueryLimitedInformation, false, processId);
        if (process.IsInvalid) return null;
        return Read(process, processId);
    }

    internal static ProcessFacts Read(SafeProcessHandle process, int processId)
    {
        _ = Native.ProcessIdToSessionId(processId, out var session);
        _ = Native.GetProcessTimes(process, out var created, out _, out _, out _);
        var (sid, integrity) = ReadToken(process);
        return new ProcessFacts(ImagePath(process), sid, integrity, session, created);
    }

    internal static string? ImagePath(SafeProcessHandle process)
    {
        var buffer = new char[1024];
        var size = (uint)buffer.Length;
        return Native.QueryFullProcessImageNameW(process, 0, buffer, ref size) ? new string(buffer, 0, (int)size) : null;
    }

    private static (string? Sid, uint Integrity) ReadToken(SafeProcessHandle process)
    {
        if (!Native.OpenProcessToken(process, Native.TokenQuery, out var token)) return (null, 0);
        using (token)
        {
            string? sid = null;
            uint integrity = 0;
            var user = TokenBuffer(token, Native.TokenUser);
            if (user != IntPtr.Zero)
            {
                try { sid = SidToString(Marshal.ReadIntPtr(user)); }
                finally { Marshal.FreeHGlobal(user); }
            }
            var label = TokenBuffer(token, Native.TokenIntegrityLevel);
            if (label != IntPtr.Zero)
            {
                try
                {
                    var labelSid = Marshal.ReadIntPtr(label);
                    var count = Marshal.ReadByte(Native.GetSidSubAuthorityCount(labelSid));
                    if (count > 0) integrity = (uint)Marshal.ReadInt32(Native.GetSidSubAuthority(labelSid, (uint)(count - 1)));
                }
                finally { Marshal.FreeHGlobal(label); }
            }
            return (sid, integrity);
        }
    }

    private static IntPtr TokenBuffer(SafeAccessTokenHandle token, int infoClass)
    {
        _ = Native.GetTokenInformation(token, infoClass, IntPtr.Zero, 0, out var needed);
        if (needed <= 0) return IntPtr.Zero;
        var buffer = Marshal.AllocHGlobal(needed);
        if (Native.GetTokenInformation(token, infoClass, buffer, needed, out _)) return buffer;
        Marshal.FreeHGlobal(buffer);
        return IntPtr.Zero;
    }

    internal static string? SidToString(IntPtr sid)
    {
        if (sid == IntPtr.Zero || !Native.ConvertSidToStringSidW(sid, out var text)) return null;
        try { return Marshal.PtrToStringUni(text); }
        finally { Native.LocalFree(text); }
    }

    /// <summary>Terminates exactly this process object (never a name or a tree). Returns false if it cannot be opened.</summary>
    public static bool TryTerminate(int processId, long expectedStartFileTime)
    {
        using var process = Native.OpenProcess(Native.ProcessQueryLimitedInformation | Native.ProcessTerminate | Native.Synchronize, false, processId);
        if (process.IsInvalid) return false;
        _ = Native.GetProcessTimes(process, out var created, out _, out _, out _);
        // The start time was checked by the caller's policy; re-check on the handle we hold so a
        // PID reused between the check and now is never terminated.
        if (created != expectedStartFileTime) return false;
        if (!Native.TerminateProcess(process, 1)) throw new Win32Exception(Marshal.GetLastWin32Error());
        _ = Native.WaitForSingleObject(process, 5000);
        return true;
    }
}

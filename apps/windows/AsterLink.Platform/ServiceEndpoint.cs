using System.IO.Pipes;
using System.Security.AccessControl;
using System.Security.Principal;
using AsterLink.Shared.Service;

namespace AsterLink.Platform;

/// <summary>What the service learned about the process on the other end of an accepted pipe.</summary>
public sealed record PipePeer(int ProcessId, int PipeSessionId, ProcessFacts? Process, string? ImpersonatedSid);

/// <summary>
/// The service side of the GUI pipe. The pipe object is created with an explicit DACL (SYSTEM and
/// Administrators full; interactive users read/write; the network logon denied outright), the first
/// instance is created with FILE_FLAG_FIRST_PIPE_INSTANCE so nobody can have squatted the name,
/// and remote clients are refused by the pipe itself. Who the caller really is gets decided
/// afterwards from OS facts, in <see cref="PeerPolicy"/>.
/// </summary>
public static class ServiceEndpoint
{
    public static PipeSecurity BuildSecurity()
    {
        var security = new PipeSecurity();
        security.SetAccessRuleProtection(isProtected: true, preserveInheritance: false);
        security.AddAccessRule(new PipeAccessRule(new SecurityIdentifier(WellKnownSidType.NetworkSid, null), PipeAccessRights.FullControl, AccessControlType.Deny));
        security.AddAccessRule(new PipeAccessRule(new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null), PipeAccessRights.FullControl, AccessControlType.Allow));
        security.AddAccessRule(new PipeAccessRule(new SecurityIdentifier(WellKnownSidType.BuiltinAdministratorsSid, null), PipeAccessRights.FullControl, AccessControlType.Allow));
        security.AddAccessRule(new PipeAccessRule(new SecurityIdentifier(WellKnownSidType.InteractiveSid, null), PipeAccessRights.ReadWrite | PipeAccessRights.Synchronize, AccessControlType.Allow));
        return security;
    }

    public static NamedPipeServerStream Create(string pipeName, bool first)
    {
        var options = PipeOptions.Asynchronous | (first ? PipeOptions.FirstPipeInstance : PipeOptions.None);
        return NamedPipeServerStreamAcl.Create(pipeName, PipeDirection.InOut, NamedPipeServerStream.MaxAllowedServerInstances,
            PipeTransmissionMode.Byte, options, 64 * 1024, 64 * 1024, BuildSecurity());
    }

    /// <summary>
    /// Must be called after the first frame was read: Windows refuses to impersonate a pipe client
    /// that has not sent anything yet. The caller identity comes from the pipe and the process
    /// token, never from the request.
    /// </summary>
    public static PipePeer Inspect(NamedPipeServerStream pipe)
    {
        var handle = pipe.SafePipeHandle;
        _ = Native.GetNamedPipeClientProcessId(handle, out var pid);
        _ = Native.GetNamedPipeClientSessionId(handle, out var session);

        string? impersonated = null;
        try
        {
            pipe.RunAsClient(() => impersonated = ProcessIdentity.ImpersonatedUserSid());
        }
        catch (IOException) { impersonated = null; }
        catch (InvalidOperationException) { impersonated = null; }
        catch (UnauthorizedAccessException) { impersonated = null; }
        catch (System.Security.SecurityException) { impersonated = null; } // an anonymous token cannot be opened for query

        return new PipePeer((int)pid, (int)session, pid == 0 ? null : ProcessIdentity.Read((int)pid), impersonated);
    }
}

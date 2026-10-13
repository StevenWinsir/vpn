using System.IO.Pipes;
using System.Security.AccessControl;
using System.Security.Principal;
using AsterLink.Platform;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;

namespace AsterLink.Tests.Windows;

public class PipeSecurityTests
{
    private static readonly SecurityIdentifier Network = new(WellKnownSidType.NetworkSid, null);
    private static readonly SecurityIdentifier Everyone = new(WellKnownSidType.WorldSid, null);
    private static readonly SecurityIdentifier Users = new(WellKnownSidType.BuiltinUsersSid, null);
    private static readonly SecurityIdentifier Authenticated = new(WellKnownSidType.AuthenticatedUserSid, null);
    private static readonly SecurityIdentifier Interactive = new(WellKnownSidType.InteractiveSid, null);

    [WindowsFact]
    public void The_dacl_denies_network_logons_and_names_no_broad_group()
    {
        var rules = ServiceEndpoint.BuildSecurity().GetAccessRules(true, true, typeof(SecurityIdentifier)).Cast<PipeAccessRule>().ToList();

        Assert.Contains(rules, r => r.IdentityReference == Network && r.AccessControlType == AccessControlType.Deny);
        Assert.Contains(rules, r => r.IdentityReference == Interactive && r.AccessControlType == AccessControlType.Allow);
        foreach (var broad in new[] { Everyone, Users, Authenticated })
            Assert.DoesNotContain(rules, r => r.IdentityReference == broad && r.AccessControlType == AccessControlType.Allow);
        Assert.True(ServiceEndpoint.BuildSecurity().AreAccessRulesProtected);
    }

    [WindowsFact]
    public void The_service_pipe_name_cannot_be_squatted_once_the_first_instance_exists()
    {
        var name = "AsterLink.Test." + Guid.NewGuid().ToString("N");
        using var first = ServiceEndpoint.Create(name, first: true);
        Assert.ThrowsAny<Exception>(() => ServiceEndpoint.Create(name, first: true).Dispose());
    }

    [WindowsFact]
    public async Task Inspect_reports_the_real_caller_from_the_os_not_from_the_request()
    {
        var name = "AsterLink.Test." + Guid.NewGuid().ToString("N");
        await using var server = ServiceEndpoint.Create(name, first: true);
        await using var client = new NamedPipeClientStream(".", name, PipeDirection.InOut, PipeOptions.Asynchronous, TokenImpersonationLevel.Identification);

        var accept = server.WaitForConnectionAsync();
        await client.ConnectAsync(10_000);
        await accept;
        await IpcFrame.WriteAsync(client, "{}"u8.ToArray());
        _ = await IpcFrame.ReadAsync(server);

        var peer = ServiceEndpoint.Inspect(server);

        using var self = WindowsIdentity.GetCurrent();
        Assert.Equal(Environment.ProcessId, peer.ProcessId);
        Assert.NotNull(peer.Process);
        Assert.Equal(self.User!.Value, peer.Process!.UserSid);
        Assert.Equal(self.User.Value, peer.ImpersonatedSid);
        Assert.Equal(Environment.ProcessPath, peer.Process.ImagePath, ignoreCase: true);
        Assert.True(peer.Process.IntegrityRid >= PeerPolicy.MediumIntegrity);
        Assert.Equal(System.Diagnostics.Process.GetCurrentProcess().SessionId, peer.PipeSessionId);
    }

    [WindowsFact]
    public async Task A_client_that_only_offers_anonymous_identity_cannot_be_identified()
    {
        var name = "AsterLink.Test." + Guid.NewGuid().ToString("N");
        await using var server = ServiceEndpoint.Create(name, first: true);
        await using var client = new NamedPipeClientStream(".", name, PipeDirection.InOut, PipeOptions.Asynchronous, TokenImpersonationLevel.Anonymous);

        var accept = server.WaitForConnectionAsync();
        await client.ConnectAsync(10_000);
        await accept;
        await IpcFrame.WriteAsync(client, "{}"u8.ToArray());
        _ = await IpcFrame.ReadAsync(server);

        var peer = ServiceEndpoint.Inspect(server);
        // Whatever the OS reports for an anonymous token, it must not equal the process owner, so the policy refuses it.
        Assert.NotEqual(peer.Process?.UserSid, peer.ImpersonatedSid);
    }

    [WindowsFact]
    public async Task A_stand_in_server_is_rejected_by_the_client_side_verification()
    {
        var name = "AsterLink.Test." + Guid.NewGuid().ToString("N");
        await using var fake = new NamedPipeServerStream(name, PipeDirection.InOut, 1, PipeTransmissionMode.Byte, PipeOptions.Asynchronous);
        await using var client = new NamedPipeClientStream(".", name, PipeDirection.InOut, PipeOptions.Asynchronous, TokenImpersonationLevel.Identification);

        var accept = fake.WaitForConnectionAsync();
        await client.ConnectAsync(10_000);
        await accept;

        // This test process is neither the registered AsterLinkService process nor SYSTEM from the protected path.
        var reason = ServiceConnection.VerifyServer(client, InstallVerifier.ProgramFilesLayout());
        Assert.NotNull(reason);
    }

    [WindowsFact]
    public void An_unregistered_service_is_reported_as_not_installed()
    {
        Assert.Equal(AsterLink.Shared.Service.ScmState.NotInstalled, ScmClient.Query("AsterLinkServiceDoesNotExist" + Guid.NewGuid().ToString("N")[..6]).State);
    }
}

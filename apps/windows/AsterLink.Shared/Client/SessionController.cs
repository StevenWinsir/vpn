using System.Text;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;
using AsterLink.Shared.Snapshot;

namespace AsterLink.Shared.Client;

public enum AppPhase
{
    /// <summary>Waiting for the service and Core to be reachable.</summary>
    Starting,
    SignedOut,
    SigningIn,
    SignedIn,
}

public enum SessionActivity { None, Connecting, Disconnecting }

public static class LoginInput
{
    public const int MaxEmailLength = 254;
    public const int MaxPasswordBytes = 72;

    /// <summary>Same limits as the Core (native/core/internal/service/service.go); null when the input is acceptable.</summary>
    public static string? Validate(string email, string password)
    {
        if (email.Trim().Length == 0 || password.Length == 0) return "请输入邮箱和密码";
        if (email.Trim().Length > MaxEmailLength) return "邮箱过长";
        if (Encoding.UTF8.GetByteCount(password) > MaxPasswordBytes) return "密码过长（最多 72 字节）";
        return null;
    }
}

/// <summary>
/// Drives the three pages. It owns no authorization logic: every verdict comes from the Core's snapshot
/// and the UI only renders it. Passwords live in a local variable for one login call and are never stored
/// on this object. Methods are meant to be called on the UI thread; continuations then return to it.
/// </summary>
public sealed class SessionController
{
    private readonly CoreClient client;
    private readonly ClientEnvironmentFacts environment;
    private readonly ICredentialStore credentials;
    private readonly ILoginPreferences preferences;
    private readonly IDeviceIdentity devices;
    private readonly TimeSpan idleInterval;
    private readonly TimeSpan liveInterval;

    private CancellationTokenSource? polling;
    private int autoRelogins;
    private long operationVersion;
    private bool cancelRequested;

    public SessionController(CoreClient client, ClientEnvironmentFacts environment, ICredentialStore credentials,
        ILoginPreferences preferences, IDeviceIdentity devices,
        TimeSpan? idleInterval = null, TimeSpan? liveInterval = null)
    {
        this.client = client;
        this.environment = environment;
        this.credentials = credentials;
        this.preferences = preferences;
        this.devices = devices;
        this.idleInterval = idleInterval ?? TimeSpan.FromSeconds(2);
        this.liveInterval = liveInterval ?? TimeSpan.FromSeconds(1);
        Email = preferences.RememberedEmail ?? "";
        RememberPassword = false;
    }

    /// <summary>Raised after any change a view might care about. Always raised on the caller's context.</summary>
    public event Action? Changed;

    public AppPhase Phase { get; private set; } = AppPhase.Starting;
    public CoreSnapshot Snapshot { get; private set; } = EmptySnapshot;
    public string? Message { get; private set; }
    public string? Warning { get; private set; }
    public bool Busy { get; private set; }

    /// <summary>Which long command is in flight; only a connect can be cancelled.</summary>
    public SessionActivity Activity { get; private set; }

    /// <summary>The Core or service cannot be reached at all (distinct from a business refusal).</summary>
    public string? ServiceProblem { get; private set; }
    public string Email { get; set; }
    public bool RememberPassword { get; set; }

    public static CoreSnapshot EmptySnapshot { get; } = new() { State = "signed_out" };

    public Uri WebsiteBase => environment.WebsiteBase;
    public Uri PlansUrl => new(environment.WebsiteBase, "/plans");
    public Uri RegisterUrl => new(environment.WebsiteBase, "/register");
    public Uri ForgotPasswordUrl => new(environment.WebsiteBase, "/forgot-password");
    public Uri DashboardUrl => new(environment.WebsiteBase, "/dashboard");
    public Uri OrdersUrl => new(environment.WebsiteBase, "/orders");

    public ConnectionView Connection => ConnectionView.From(Snapshot, Busy);

    private ulong CurrentGeneration => (ulong)Math.Max(0, Snapshot.Generation);

    private string AccountFor(string email) => CredentialAccount.Key(environment.ApiBase, email);

    private void Raise() => Changed?.Invoke();

    // ---- launch ------------------------------------------------------------------------------

    /// <summary>
    /// Called once at start. A remembered password only saves typing: the Core still authenticates
    /// online, so an expired plan or a changed password is detected right now.
    /// </summary>
    public async Task LaunchAsync()
    {
        if (Phase != AppPhase.Starting) return;
        try
        {
            await client.StartAsync().ConfigureAwait(true);
            ServiceProblem = null;
        }
        catch (Exception error) when (IsTransportFailure(error))
        {
            ServiceProblem = Describe(error);
            Phase = AppPhase.SignedOut;
            Raise();
            return;
        }
        Phase = AppPhase.SignedOut;
        Raise();

        if (Email.Length == 0) return;
        string? saved;
        try { saved = credentials.Read(AccountFor(Email)); }
        catch (CredentialStoreException)
        {
            Warning = "无法读取 Windows 凭据管理器，请手动输入密码";
            Raise();
            return;
        }
        if (!string.IsNullOrEmpty(saved))
        {
            RememberPassword = true;
            await SignInAsync(Email, saved, remember: true, automatic: true).ConfigureAwait(true);
        }
    }

    /// <summary>Start-up found the privileged service not ready (the card says why): go to the login page and wait for it.</summary>
    public void ServiceUnavailable(string problem)
    {
        if (Phase != AppPhase.Starting) return;
        ServiceProblem = problem;
        Phase = AppPhase.SignedOut;
        Raise();
    }

    /// <summary>The service was just installed or repaired after launch found it unreachable: retry the start-up once.</summary>
    public async Task ServiceBecameAvailableAsync()
    {
        if (Phase != AppPhase.SignedOut || ServiceProblem is null || Busy) return;
        ServiceProblem = null;
        Message = null;
        Phase = AppPhase.Starting;
        Raise();
        await LaunchAsync().ConfigureAwait(true);
    }

    // ---- sign in / out -----------------------------------------------------------------------

    public async Task SignInAsync(string rawEmail, string password, bool remember, bool automatic = false)
    {
        var email = rawEmail.Trim();
        if (LoginInput.Validate(email, password) is { } problem)
        {
            Message = problem;
            Raise();
            return;
        }
        if (Busy) return;
        Busy = true;
        Message = null;
        Warning = null;
        Phase = AppPhase.SigningIn;
        Email = email;
        operationVersion++;
        Raise();
        try
        {
            await client.StartAsync().ConfigureAwait(true);
            ServiceProblem = null;
            var reply = await client.LoginAsync(email, password, devices.DeviceId()).ConfigureAwait(true);
            Snapshot = reply.Snapshot;
            if (reply.ActionError is { } failure)
            {
                Phase = AppPhase.SignedOut;
                Message = failure.Message;
                if (failure.Code == "invalid_credentials") ForgetRejected(email, password);
                return;
            }
            Phase = AppPhase.SignedIn;
            Persist(email, password, remember);
            // Only a deliberate sign-in re-arms the single silent re-login, so a server that keeps
            // expiring sessions cannot make the app hammer the login endpoint.
            if (!automatic) autoRelogins = 0;
            StartPolling();
        }
        catch (Exception error) when (IsTransportFailure(error) || error is IpcCommandException)
        {
            Phase = AppPhase.SignedOut;
            var text = Describe(error);
            if (IsTransportFailure(error)) ServiceProblem = text;
            Message = text;
        }
        finally
        {
            Busy = false;
            Raise();
        }
    }

    /// <summary>A credential-vault failure never downgrades to a file: the user is told it was not remembered.</summary>
    private void Persist(string email, string password, bool remember)
    {
        var key = AccountFor(email);
        if (remember)
        {
            try
            {
                credentials.Save(key, password);
                preferences.RememberedEmail = email;
                RememberPassword = true;
            }
            catch (CredentialStoreException)
            {
                Warning = "无法保存到 Windows 凭据管理器，本次未记住密码";
                RememberPassword = false;
                TryDelete(key);
            }
        }
        else
        {
            TryDelete(key);
            RememberPassword = false;
            if (string.Equals(preferences.RememberedEmail, email, StringComparison.OrdinalIgnoreCase)) preferences.RememberedEmail = null;
        }
    }

    /// <summary>
    /// A saved password the server rejects is stale (changed on the website, for example): stop offering
    /// it. Only the item that still holds exactly the rejected password goes, so a typo in a manual login
    /// never drops a good saved password, and a password saved meanwhile by a newer login survives. The
    /// email and the device identity are kept.
    /// </summary>
    private void ForgetRejected(string email, string password)
    {
        var key = AccountFor(email);
        string? saved;
        try { saved = credentials.Read(key); }
        catch (CredentialStoreException) { return; }
        if (string.Equals(saved, password, StringComparison.Ordinal)) TryDelete(key);
    }

    private void TryDelete(string key)
    {
        try { credentials.Delete(key); }
        catch (CredentialStoreException) { Warning = "无法从 Windows 凭据管理器删除已保存的密码，请在“凭据管理器”中手动删除"; }
    }

    /// <summary>Explicit sign-out also forgets the saved password for this account.</summary>
    public async Task SignOutAsync()
    {
        StopPolling();
        operationVersion++;
        var key = AccountFor(Snapshot.User?.Email is { Length: > 0 } known ? known : Email);
        Phase = AppPhase.SignedOut;
        Raise();
        try { Snapshot = await client.LogoutAsync().ConfigureAwait(true); }
        catch (Exception error) when (IsTransportFailure(error) || error is IpcCommandException) { Snapshot = EmptySnapshot; }
        TryDelete(key);
        preferences.RememberedEmail = null;
        RememberPassword = false;
        Message = null;
        Raise();
    }

    /// <summary>Normal app exit: stop and settle, but keep a password the user chose to remember.</summary>
    public async Task ShutdownAsync()
    {
        StopPolling();
        if (Phase == AppPhase.SignedIn && Snapshot.State is "connected" or "connecting" or "preparing")
        {
            try
            {
                using var limit = new CancellationTokenSource(TimeSpan.FromSeconds(20));
                await client.DisconnectAsync(limit.Token).ConfigureAwait(true);
            }
            catch (Exception error) when (IsTransportFailure(error) || error is IpcCommandException or OperationCanceledException) { }
        }
        await client.StopAsync().ConfigureAwait(true);
    }

    // ---- actions -----------------------------------------------------------------------------

    public async Task RefreshAsync()
    {
        if (Phase != AppPhase.SignedIn || Busy) return;
        await RunAsync(() => client.RefreshCatalogAsync(CurrentGeneration)).ConfigureAwait(true);
    }

    public async Task SelectAsync(string nodeId)
    {
        if (Phase != AppPhase.SignedIn || Busy || Snapshot.State is "connected" or "connecting" or "preparing" or "stopping") return;
        if (nodeId == Snapshot.SelectedNodeId && Snapshot.ConfigReady) return;
        await RunAsync(() => client.SelectNodeAsync(CurrentGeneration, nodeId, Snapshot.CatalogRevision)).ConfigureAwait(true);
    }

    public async Task ConnectAsync()
    {
        if (Phase != AppPhase.SignedIn || Busy || !Connection.CanConnect) return;
        Activity = SessionActivity.Connecting;
        try { await RunAsync(() => client.ConnectAsync(CurrentGeneration)).ConfigureAwait(true); }
        finally { Activity = SessionActivity.None; Raise(); }
    }

    public async Task DisconnectAsync()
    {
        if (Phase != AppPhase.SignedIn || Busy) return;
        Busy = true;
        Activity = SessionActivity.Disconnecting;
        operationVersion++;
        Raise();
        try
        {
            // Closing the data path also settles the last traffic batch, which can take a few seconds on a slow network.
            var updated = await client.DisconnectAsync().ConfigureAwait(true);
            Apply(updated);
            Message = updated.LastError?.Message;
        }
        catch (Exception error) when (error is IpcCommandException or CoreUnavailableException or IOException)
        {
            if (IsWorkerLost(error)) WorkerLost();
            else Message = Describe(error);
        }
        finally
        {
            Busy = false;
            Activity = SessionActivity.None;
            Raise();
        }
    }

    /// <summary>
    /// Abandons a connect that is taking too long. The command is already inside the Core, so the only
    /// reliable way back to a known state is to end the whole session: the pipe is closed, the service stops
    /// the Core (which settles and revokes), and the user signs in again.
    /// </summary>
    public async Task CancelOperationAsync()
    {
        if (!Busy || Phase != AppPhase.SignedIn) return;
        cancelRequested = true;
        StopPolling();
        try { await client.StopAsync().ConfigureAwait(true); }
        catch (Exception error) when (IsTransportFailure(error) || error is IOException) { }
    }

    private async Task RunAsync(Func<Task<SnapshotReply>> action)
    {
        Busy = true;
        cancelRequested = false;
        operationVersion++;
        Raise();
        try
        {
            var reply = await action().ConfigureAwait(true);
            Apply(reply.Snapshot);
            Message = reply.ActionError?.Message;
        }
        catch (IpcCommandException error) when (error.Code == "stale_generation")
        {
            // The state changed under the click: show the new state and let the user decide again.
            try { Apply(await client.ReadSnapshotAsync().ConfigureAwait(true)); } catch (Exception inner) when (IsTransportFailure(inner) || inner is IpcCommandException) { }
            Message = error.Message.Length > 0 ? CleanMessage(error) : "状态已变化，请重试";
        }
        catch (Exception error) when (error is IpcCommandException or CoreUnavailableException or IOException or ObjectDisposedException)
        {
            if (cancelRequested) CancelledByUser();
            else if (IsWorkerLost(error)) WorkerLost();
            else Message = Describe(error);
        }
        finally
        {
            Busy = false;
            Raise();
        }
    }

    // ---- polling -----------------------------------------------------------------------------

    private void StartPolling()
    {
        StopPolling();
        var source = new CancellationTokenSource();
        polling = source;
        _ = PollLoopAsync(source.Token);
    }

    private void StopPolling()
    {
        polling?.Cancel();
        polling = null;
    }

    private async Task PollLoopAsync(CancellationToken token)
    {
        while (!token.IsCancellationRequested)
        {
            // Live rates are only worth a faster read while a tunnel is carrying traffic.
            var interval = Snapshot.State == "connected" ? liveInterval : idleInterval;
            try { await Task.Delay(interval, token).ConfigureAwait(true); }
            catch (OperationCanceledException) { return; }
            if (token.IsCancellationRequested) return;
            await PollAsync().ConfigureAwait(true);
        }
    }

    /// <summary>One snapshot read. A result that raced with a user action is discarded, never applied over newer state.</summary>
    public async Task PollAsync()
    {
        if (Phase != AppPhase.SignedIn || Busy) return;
        var version = operationVersion;
        try
        {
            var latest = await client.ReadSnapshotAsync().ConfigureAwait(true);
            if (version != operationVersion || Phase != AppPhase.SignedIn) return;
            ServiceProblem = null;
            Apply(latest);
        }
        catch (Exception error) when (error is IpcCommandException or CoreUnavailableException or IOException or ObjectDisposedException)
        {
            if (version != operationVersion) return;
            if (IsWorkerLost(error)) WorkerLost();
            else { ServiceProblem = Describe(error); Raise(); }
        }
    }

    /// <summary>
    /// The service reported that the Core exited or its pipe broke. The session, token and tunnel lived
    /// only in that process, so nothing is resumed: the user signs in online again, which starts a fresh Core.
    /// </summary>
    private void WorkerLost()
    {
        StopPolling();
        Phase = AppPhase.SignedOut;
        Snapshot = EmptySnapshot;
        Message = "内核已意外退出，连接已停止，请重新登录";
        Raise();
    }

    private void CancelledByUser()
    {
        StopPolling();
        Phase = AppPhase.SignedOut;
        Snapshot = EmptySnapshot;
        Message = "已取消。本次会话已结束，请重新登录";
        Raise();
    }

    private void Apply(CoreSnapshot latest)
    {
        var previous = Snapshot;
        // A snapshot from an older generation of the same session is late news, not state.
        if (Phase == AppPhase.SignedIn && latest.State != "signed_out" && latest.Generation < previous.Generation) return;
        Snapshot = latest;
        Raise();
        if (Phase != AppPhase.SignedIn || latest.State != "signed_out" || previous.State == "signed_out") return;

        // The Core ended the session (expired, revoked, plan changed).
        StopPolling();
        Phase = AppPhase.SignedOut;
        Message = latest.LastError?.Message is { Length: > 0 } text ? text : "登录已失效，请重新登录";
        var code = latest.LastError?.Code ?? "";
        Raise();
        if (!code.StartsWith("native_session_", StringComparison.Ordinal) || autoRelogins >= 1) return;
        string? saved;
        try { saved = credentials.Read(AccountFor(Email)); }
        catch (CredentialStoreException) { return; }
        if (string.IsNullOrEmpty(saved)) return;
        // One silent re-login for an expired session; a refused password is never retried.
        autoRelogins++;
        _ = SignInAsync(Email, saved, remember: true, automatic: true);
    }

    // ---- error classification ----------------------------------------------------------------

    private static bool IsTransportFailure(Exception error) =>
        error is CoreUnavailableException or IOException or ObjectDisposedException or System.Text.Json.JsonException
        || error is IpcCommandException command && IsServiceCode(command.Code);

    private static bool IsServiceCode(string code) =>
        code is ServiceErrors.ServiceNotInstalled or ServiceErrors.ServiceNotRunning or ServiceErrors.ServerRejected
            or ServiceErrors.PeerRejected or ServiceErrors.OwnerBusy or ServiceErrors.VersionMismatch
            or ServiceErrors.IntegrityFailed or ServiceErrors.RecoveryBlocked or ServiceErrors.WorkerUnavailable
            or ServiceErrors.WorkerExited or ServiceErrors.WorkerUnresponsive or ServiceErrors.LoginTargetMismatch;

    private static bool IsWorkerLost(Exception error) => error switch
    {
        IpcCommandException command => ErrorText.IsWorkerLost(command.Code),
        CoreUnavailableException unavailable => ErrorText.IsWorkerLost(unavailable.Code),
        IOException or ObjectDisposedException => true,
        _ => false,
    };

    private static string CleanMessage(IpcCommandException error) =>
        error.Message.StartsWith(error.Code + ": ", StringComparison.Ordinal) ? error.Message[(error.Code.Length + 2)..] : error.Message;

    private static string Describe(Exception error) => error switch
    {
        IpcCommandException command when IsServiceCode(command.Code) => ErrorText.ForService(command.Code),
        IpcCommandException command => CleanMessage(command) is { Length: > 0 } text ? text : "操作失败，请稍后重试",
        CoreUnavailableException unavailable => ErrorText.ForService(unavailable.Code, unavailable.Message),
        _ => ErrorText.ForService(""),
    };
}

/// <summary>The public build parameters the controller needs, without depending on the parser.</summary>
public sealed record ClientEnvironmentFacts(Uri ApiBase, Uri WebsiteBase, string Channel)
{
    public static ClientEnvironmentFacts From(Env.ClientEnvironment environment) =>
        new(environment.ApiBaseUrl, environment.WebsiteBaseUrl, environment.AppEnv);
}

using System.Text.Json;
using System.Text.Json.Nodes;
using AsterLink.Shared.Client;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;
using AsterLink.Shared.Snapshot;

namespace AsterLink.Tests;

/// <summary>Scripted Core: each command is answered by a delegate, and every command that reached it is recorded.</summary>
internal sealed class FakeTransport : ICoreTransport
{
    public record Sent(string Command, ulong Generation, JsonElement? Payload);

    public List<Sent> Log { get; } = [];
    public Func<string, ulong, JsonElement?, Task<IpcResponse>>? Handler { get; set; }
    public Exception? StartFailure { get; set; }
    public int Starts { get; private set; }
    public int Stops { get; private set; }

    public Task StartAsync(CancellationToken cancellationToken = default)
    {
        Starts++;
        return StartFailure is null ? Task.CompletedTask : Task.FromException(StartFailure);
    }

    public async Task<IpcResponse> SendAsync(string command, ulong generation, JsonElement? payload, CancellationToken cancellationToken = default)
    {
        lock (Log) Log.Add(new Sent(command, generation, payload));
        return await Handler!(command, generation, payload);
    }

    public Task StopAsync()
    {
        Stops++;
        return Task.CompletedTask;
    }

    public IEnumerable<Sent> Commands(string name)
    {
        lock (Log) return Log.Where(s => s.Command == name).ToList();
    }
}

internal static class Replies
{
    public static IpcResponse Snapshot(CoreSnapshot snapshot, ErrorView? actionError = null)
    {
        var node = JsonSerializer.SerializeToNode(snapshot, SnapshotJson.Options)!.AsObject();
        node["action_error"] = actionError is null ? null : JsonSerializer.SerializeToNode(actionError, SnapshotJson.Options);
        return new IpcResponse { RequestId = "x", ProtocolVersion = 1, Ok = true, Result = JsonSerializer.SerializeToElement(node) };
    }

    public static IpcResponse Refused(string code, string message) =>
        new() { RequestId = "x", ProtocolVersion = 1, Ok = false, Error = new IpcError { Code = code, Message = message } };

    public static CoreSnapshot Plan(string state = "ready", long generation = 5, bool authorized = true, string selected = "", bool configReady = false,
        IReadOnlyList<NodeView>? nodes = null, string role = "user", ErrorView? lastError = null, string reason = "") => new()
    {
        Generation = generation,
        State = state,
        User = new UserView { Id = "u1", Email = "user@example.com", Name = "User", Role = role },
        Entitlement = new EntitlementView
        {
            PlanName = authorized ? "VIP" : "",
            AccountAuthorized = authorized,
            CanConnect = authorized && selected.Length > 0,
            Reason = reason,
            ReasonMessage = authorized ? "" : "当前账号没有有效套餐，请前往网站购买",
            TotalBytes = 100L << 30,
            RemainingBytes = 60L << 30,
            SubscriptionExpiresAt = new DateTimeOffset(2030, 1, 1, 0, 0, 0, TimeSpan.Zero),
        },
        Nodes = nodes ?? Nodes(),
        SelectedNodeId = selected,
        CatalogRevision = new string('a', 64),
        ConfigReady = configReady,
        LastError = lastError,
        CoreVersion = "0.1.0",
    };

    public const string NodeA = "11111111-1111-1111-1111-111111111111";
    public const string NodeB = "22222222-2222-2222-2222-222222222222";

    public static IReadOnlyList<NodeView> Nodes() =>
    [
        new NodeView { Id = NodeA, Name = "东京专线", Region = "日本", LineType = "dedicated", RatePermille = 1000 },
        new NodeView { Id = NodeB, Name = "香港普通", Region = "香港", LineType = "direct", RatePermille = 500 },
    ];

    public static CoreSnapshot SignedOut(ErrorView? error = null) => new() { Generation = 6, State = "signed_out", LastError = error };
}

internal sealed class MemoryCredentials : ICredentialStore
{
    public Dictionary<string, string> Items { get; } = [];
    public bool FailSave { get; set; }
    public bool FailRead { get; set; }
    public bool FailDelete { get; set; }

    public void Save(string account, string password)
    {
        if (FailSave) throw new CredentialStoreException("vault unavailable");
        Items[account] = password;
    }

    public string? Read(string account)
    {
        if (FailRead) throw new CredentialStoreException("vault unavailable");
        return Items.GetValueOrDefault(account);
    }

    public void Delete(string account)
    {
        if (FailDelete) throw new CredentialStoreException("vault unavailable");
        Items.Remove(account);
    }
}

internal sealed class MemoryPreferences : ILoginPreferences
{
    public string? RememberedEmail { get; set; }
    public string Theme { get; set; } = "system";
    public bool TrayNoticeShown { get; set; }
}

internal sealed class FixedDevice : IDeviceIdentity
{
    public string DeviceId() => "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
}

internal sealed class Harness
{
    public static readonly Uri Api = new("https://api.example.test/api/v1/client");

    public FakeTransport Transport { get; } = new();
    public MemoryCredentials Credentials { get; } = new();
    public MemoryPreferences Preferences { get; } = new();
    public SessionController Session { get; }

    public Harness(string? rememberedEmail = null)
    {
        Preferences.RememberedEmail = rememberedEmail;
        var client = new CoreClient(Transport, Api.AbsoluteUri.TrimEnd('/'), "development", "0.1.0");
        var facts = new ClientEnvironmentFacts(Api, new Uri("https://site.example.test"), "development");
        // Polling is driven by hand in the tests: a one-hour interval never fires on its own.
        Session = new SessionController(client, facts, Credentials, Preferences, new FixedDevice(), TimeSpan.FromHours(1), TimeSpan.FromHours(1));
    }

    public string Key(string email) => CredentialAccount.Key(Api, email);

    /// <summary>Answers login with <paramref name="snapshot"/>, everything else via <paramref name="rest"/>.</summary>
    public void Script(CoreSnapshot loginSnapshot, Func<string, ulong, JsonElement?, IpcResponse>? rest = null, ErrorView? loginError = null)
    {
        Transport.Handler = (command, generation, payload) => Task.FromResult(command == IpcCommands.Login
            ? Replies.Snapshot(loginSnapshot, loginError)
            : rest?.Invoke(command, generation, payload) ?? Replies.Snapshot(loginSnapshot));
    }
}

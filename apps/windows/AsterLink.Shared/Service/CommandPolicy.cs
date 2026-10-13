using System.Text.Json;
using AsterLink.Shared.Env;
using AsterLink.Shared.Ipc;

namespace AsterLink.Shared.Service;

/// <summary>A request the service has decided to look at further. It carries no payload copy.</summary>
public sealed record AcceptedRequest(string RequestId, ulong Generation, string Command, JsonElement? Payload);

public sealed record CommandDecision(AcceptedRequest? Accepted, string? ErrorCode, string? Message, string RequestId = "", ulong Generation = 0)
{
    public bool Allowed => Accepted is not null;
}

/// <summary>
/// What the GUI is allowed to send through the service. The service forwards a command to
/// the Core only after this check: known name, bounded size (the same per-command limits as
/// the Go Core, so an oversize request is refused here before it consumes Core time), strict
/// shape, and for <c>login</c> the API target pinned to the protected build configuration.
/// There is no command that carries a path, a shell string, a YAML document or a port.
/// </summary>
public static class CommandPolicy
{
    /// <summary>Same table as native/core/internal/ipc/protocol.go perCommandLimit.</summary>
    public static readonly IReadOnlyDictionary<string, int> Limits = new Dictionary<string, int>
    {
        [IpcCommands.Hello] = 4 << 10,
        [IpcCommands.Shutdown] = 1 << 10,
        [IpcCommands.ReadSnapshot] = 1 << 10,
        [IpcCommands.RefreshCatalog] = 1 << 10,
        [IpcCommands.SelectNode] = 4 << 10,
        [IpcCommands.Connect] = 4 << 10,
        [IpcCommands.Disconnect] = 1 << 10,
        [IpcCommands.Logout] = 1 << 10,
        [IpcCommands.Login] = 16 << 10,
    };

    /// <summary>Largest request the service will read at all; anything bigger ends the connection.</summary>
    public static readonly int MaxRequestFrame = Limits.Values.Max();

    private static readonly HashSet<string> Envelope = ["request_id", "protocol_version", "generation", "command", "payload"];

    public static CommandDecision Evaluate(ReadOnlySpan<byte> frame, ClientEnvironment buildConfig)
    {
        JsonDocument document;
        try
        {
            document = JsonDocument.Parse(frame.ToArray(), new JsonDocumentOptions { MaxDepth = 8 });
        }
        catch (JsonException)
        {
            return Refuse(ServiceErrors.InvalidRequest, "malformed request");
        }

        using (document)
        {
            var root = document.RootElement;
            if (root.ValueKind != JsonValueKind.Object) return Refuse(ServiceErrors.InvalidRequest, "malformed request");

            var names = new HashSet<string>(StringComparer.Ordinal);
            foreach (var property in root.EnumerateObject())
            {
                // A duplicate name could make the service and the Core read different values.
                if (!Envelope.Contains(property.Name) || !names.Add(property.Name))
                    return Refuse(ServiceErrors.InvalidRequest, "malformed request");
            }

            if (!root.TryGetProperty("request_id", out var id) || id.ValueKind != JsonValueKind.String
                || id.GetString() is not { Length: > 0 and <= 64 } requestId)
                return Refuse(ServiceErrors.InvalidRequest, "request_id required (<=64 bytes)");

            // TryGet* throws on a value of the wrong JSON type, so the type is checked first.
            var protocol = 0;
            if (!root.TryGetProperty("protocol_version", out var version) || version.ValueKind != JsonValueKind.Number
                || !version.TryGetInt32(out protocol))
                return Refuse(ServiceErrors.InvalidRequest, "malformed request", requestId);
            ulong generation = 0;
            if (root.TryGetProperty("generation", out var gen)
                && (gen.ValueKind != JsonValueKind.Number || !gen.TryGetUInt64(out generation)))
                return Refuse(ServiceErrors.InvalidRequest, "malformed request", requestId);

            if (!root.TryGetProperty("command", out var cmd) || cmd.ValueKind != JsonValueKind.String)
                return Refuse(ServiceErrors.InvalidRequest, "malformed request", requestId, generation);
            var command = cmd.GetString()!;

            if (!Limits.TryGetValue(command, out var limit))
                return Refuse(ServiceErrors.UnknownCommand, "unknown command", requestId, generation);
            if (frame.Length > limit)
                return Refuse(ServiceErrors.PayloadTooLarge, "request too large for command", requestId, generation);
            if (protocol != IpcProtocol.Version)
                return Refuse(ServiceErrors.ProtocolUnsupported, "protocol version not supported", requestId, generation);

            JsonElement? payload = root.TryGetProperty("payload", out var p) ? p.Clone() : null;
            if (command == IpcCommands.Login)
            {
                var mismatch = CheckLoginTarget(payload, buildConfig);
                if (mismatch is not null) return Refuse(ServiceErrors.LoginTargetMismatch, mismatch, requestId, generation);
            }
            return new CommandDecision(new AcceptedRequest(requestId, generation, command, payload), null, null, requestId, generation);
        }
    }

    /// <summary>
    /// The GUI may not choose which API the password is sent to: the Core is allowed to log
    /// in only against the address frozen in the protected build configuration.
    /// </summary>
    private static string? CheckLoginTarget(JsonElement? payload, ClientEnvironment buildConfig)
    {
        if (payload is not { ValueKind: JsonValueKind.Object } body) return "login parameters missing";
        if (!body.TryGetProperty("api_base", out var api) || api.ValueKind != JsonValueKind.String
            || !body.TryGetProperty("channel", out var channel) || channel.ValueKind != JsonValueKind.String)
            return "login target missing";

        var expected = buildConfig.ApiBaseUrl.AbsoluteUri.TrimEnd('/');
        if (!string.Equals(api.GetString()?.TrimEnd('/'), expected, StringComparison.Ordinal))
            return "login target is not the configured API";
        if (!string.Equals(channel.GetString(), buildConfig.AppEnv, StringComparison.Ordinal))
            return "login channel is not the build channel";
        return null;
    }

    private static CommandDecision Refuse(string code, string message, string requestId = "", ulong generation = 0) =>
        new(null, code, message, requestId, generation);
}

using System.Text.Json;
using AsterLink.Shared.Env;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Snapshot;

namespace AsterLink.Shared.Platform;

public sealed record SelfCheckResult(string Name, bool Passed, string Detail);

/// <summary>
/// A tiny in-binary check that the shared contract code behaves on the machine the binary
/// really runs on. It is evidence for the round-1 report only; it is not a substitute for
/// the xUnit suite and proves nothing about TUN, privilege or networking.
/// </summary>
public static class FoundationSelfCheck
{
    // 2^53 + 1 cannot be represented as a double; it must survive as an exact integer.
    private const long BeyondDoublePrecision = 9_007_199_254_740_993;

    public static IReadOnlyList<SelfCheckResult> Run() =>
    [
        Check("snapshot_int64_exact", SnapshotKeepsLargeIntegers),
        Check("snapshot_rfc3339_nanoseconds", SnapshotParsesGoTimestamps),
        Check("ipc_frame_roundtrip", FrameRoundTrips),
        Check("ipc_frame_rejects_oversize", FrameRejectsOversize),
        Check("env_example_accepted", EnvExampleAccepted),
        Check("env_unknown_key_rejected", EnvRejectsUnknownKey),
    ];

    private static SelfCheckResult Check(string name, Func<string> body)
    {
        try { return new SelfCheckResult(name, true, body()); }
        catch (Exception error) { return new SelfCheckResult(name, false, error.GetType().Name + ": " + error.Message); }
    }

    private static string SnapshotKeepsLargeIntegers()
    {
        var snapshot = SnapshotJson.ParseSnapshot(
            """{"generation":1,"state":"ready","entitlement":{"used_units":""" + BeyondDoublePrecision + "}}");
        return snapshot.Entitlement?.UsedUnits == BeyondDoublePrecision
            ? "used_units kept exactly"
            : throw new InvalidOperationException("used_units lost precision");
    }

    private static string SnapshotParsesGoTimestamps()
    {
        var snapshot = SnapshotJson.ParseSnapshot(
            """{"generation":1,"state":"ready","entitlement":{"subscription_expires_at":"2030-01-01T00:10:00.123456789Z"}}""");
        return snapshot.Entitlement?.SubscriptionExpiresAt?.UtcDateTime.Year == 2030
            ? "9-digit fraction accepted"
            : throw new InvalidOperationException("timestamp not parsed");
    }

    private static string FrameRoundTrips()
    {
        var request = IpcJson.SerializeRequest(new IpcRequest { RequestId = "selfcheck", Command = IpcCommands.Hello });
        using var stream = new MemoryStream();
        IpcFrame.WriteAsync(stream, request).GetAwaiter().GetResult();
        stream.Position = 0;
        var echoed = IpcFrame.ReadAsync(stream).GetAwaiter().GetResult();
        return echoed.AsSpan().SequenceEqual(request) ? "frame identical after round trip" : throw new InvalidOperationException("frame changed");
    }

    private static string FrameRejectsOversize()
    {
        // Declared length 2 MiB + 1 with no body: must be refused before allocating anything.
        using var stream = new MemoryStream([0x00, 0x20, 0x00, 0x01]);
        try { IpcFrame.ReadAsync(stream).GetAwaiter().GetResult(); }
        catch (IpcFrameException error) when (error.Error == IpcFrameError.TooLarge) { return "oversize header refused"; }
        throw new InvalidOperationException("oversize frame was not refused");
    }

    private static string EnvExampleAccepted()
    {
        var env = ClientEnvironment.Parse(
            "APP_ENV=development\nAPI_BASE_URL=https://demo.hyshentou.cn/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn\n");
        return env.IsRelease ? throw new InvalidOperationException("unexpected channel") : "development sample parsed";
    }

    private static string EnvRejectsUnknownKey()
    {
        try
        {
            ClientEnvironment.Parse(
                "APP_ENV=development\nAPI_BASE_URL=https://demo.hyshentou.cn/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn\nDATABASE_URL=x\n");
        }
        catch (ClientEnvironmentException) { return "unknown key refused"; }
        throw new InvalidOperationException("unknown key accepted");
    }

    public static string ToJson(IReadOnlyList<SelfCheckResult> results) =>
        JsonSerializer.Serialize(results, new JsonSerializerOptions { PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower, WriteIndented = true });
}

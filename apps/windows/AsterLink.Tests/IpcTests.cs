using System.Text;
using System.Text.Json;
using AsterLink.Shared.Ipc;

namespace AsterLink.Tests;

public class IpcFrameTests
{
    private static async Task<IpcFrameException> Refused(byte[] bytes, int limit = IpcFrame.MaxFrame)
    {
        using var stream = new MemoryStream(bytes);
        return await Assert.ThrowsAsync<IpcFrameException>(() => IpcFrame.ReadAsync(stream, limit));
    }

    [Fact]
    public async Task Header_is_four_byte_big_endian()
    {
        using var stream = new MemoryStream();
        await IpcFrame.WriteAsync(stream, new byte[0x0102]);
        var bytes = stream.ToArray();
        Assert.Equal([0x00, 0x00, 0x01, 0x02], bytes[..4]);
        Assert.Equal(4 + 0x0102, bytes.Length);
    }

    [Fact]
    public async Task Frame_round_trips()
    {
        var body = Encoding.UTF8.GetBytes("""{"request_id":"r1"}""");
        using var stream = new MemoryStream();
        await IpcFrame.WriteAsync(stream, body);
        stream.Position = 0;
        Assert.Equal(body, await IpcFrame.ReadAsync(stream));
    }

    [Fact]
    public async Task Two_frames_in_a_row_are_read_independently()
    {
        using var stream = new MemoryStream();
        await IpcFrame.WriteAsync(stream, "a"u8.ToArray());
        await IpcFrame.WriteAsync(stream, "bc"u8.ToArray());
        stream.Position = 0;
        Assert.Equal("a"u8.ToArray(), await IpcFrame.ReadAsync(stream));
        Assert.Equal("bc"u8.ToArray(), await IpcFrame.ReadAsync(stream));
        Assert.Equal(IpcFrameError.EndOfStream, (await Refused([])).Error);
    }

    [Fact]
    public async Task Clean_end_of_stream_is_distinct_from_truncation()
    {
        Assert.Equal(IpcFrameError.EndOfStream, (await Refused([])).Error);
        Assert.Equal(IpcFrameError.Truncated, (await Refused([0x00, 0x00])).Error);
        Assert.Equal(IpcFrameError.Truncated, (await Refused([0x00, 0x00, 0x00, 0x05, 0x61])).Error);
    }

    [Fact]
    public async Task Empty_frames_are_refused_both_ways()
    {
        Assert.Equal(IpcFrameError.Empty, (await Refused([0, 0, 0, 0])).Error);
        using var stream = new MemoryStream();
        var error = await Assert.ThrowsAsync<IpcFrameException>(() => IpcFrame.WriteAsync(stream, ReadOnlyMemory<byte>.Empty));
        Assert.Equal(IpcFrameError.Empty, error.Error);
    }

    [Fact]
    public async Task Declared_length_over_the_limit_is_refused_before_any_body_is_read()
    {
        // 2 MiB + 1 declared, no body present: must fail on the header alone.
        Assert.Equal(IpcFrameError.TooLarge, (await Refused([0x00, 0x20, 0x00, 0x01])).Error);
        // A per-command limit smaller than the absolute ceiling is honoured.
        Assert.Equal(IpcFrameError.TooLarge, (await Refused([0x00, 0x00, 0x10, 0x01], limit: 4096)).Error);
        // 0xFFFFFFFF must not wrap into a negative int.
        Assert.Equal(IpcFrameError.TooLarge, (await Refused([0xFF, 0xFF, 0xFF, 0xFF])).Error);
    }

    [Fact]
    public async Task A_frame_of_exactly_the_ceiling_is_accepted()
    {
        using var stream = new MemoryStream();
        await IpcFrame.WriteAsync(stream, new byte[IpcFrame.MaxFrame]);
        stream.Position = 0;
        Assert.Equal(IpcFrame.MaxFrame, (await IpcFrame.ReadAsync(stream)).Length);
        var error = await Assert.ThrowsAsync<IpcFrameException>(() => IpcFrame.WriteAsync(new MemoryStream(), new byte[IpcFrame.MaxFrame + 1]));
        Assert.Equal(IpcFrameError.TooLarge, error.Error);
    }

    [Fact]
    public async Task A_dribbling_stream_is_reassembled()
    {
        var body = Encoding.UTF8.GetBytes("""{"request_id":"slow"}""");
        using var whole = new MemoryStream();
        await IpcFrame.WriteAsync(whole, body);
        using var slow = new OneByteStream(whole.ToArray());
        Assert.Equal(body, await IpcFrame.ReadAsync(slow));
    }

    private sealed class OneByteStream(byte[] data) : MemoryStream(data)
    {
        public override ValueTask<int> ReadAsync(Memory<byte> buffer, CancellationToken cancellationToken = default) =>
            base.ReadAsync(buffer[..Math.Min(1, buffer.Length)], cancellationToken);
    }
}

public class IpcMessageTests
{
    [Fact]
    public void Hello_request_uses_the_wire_field_names_and_omits_an_empty_payload()
    {
        var json = Encoding.UTF8.GetString(IpcJson.SerializeRequest(new IpcRequest { RequestId = "r1", Generation = 7, Command = IpcCommands.Hello }));
        using var document = JsonDocument.Parse(json);
        var root = document.RootElement;
        Assert.Equal("r1", root.GetProperty("request_id").GetString());
        Assert.Equal(1, root.GetProperty("protocol_version").GetInt32());
        Assert.Equal(7UL, root.GetProperty("generation").GetUInt64());
        Assert.Equal("hello", root.GetProperty("command").GetString());
        Assert.False(root.TryGetProperty("payload", out _));
        Assert.Equal(4, root.EnumerateObject().Count());
    }

    [Fact]
    public void Command_names_match_the_contract()
    {
        var contract = ContractFiles.SharedContract("ipc-v1.json").GetProperty("commands").EnumerateArray().Select(e => e.GetString()).ToHashSet();
        var ours = new[]
        {
            IpcCommands.Hello, IpcCommands.Login, IpcCommands.ReadSnapshot, IpcCommands.RefreshCatalog,
            IpcCommands.SelectNode, IpcCommands.Connect, IpcCommands.Disconnect, IpcCommands.Logout, IpcCommands.Shutdown,
        };
        Assert.True(contract.SetEquals(ours));
        Assert.Equal(1, ContractFiles.SharedContract("ipc-v1.json").GetProperty("protocol_version").GetInt32());
        Assert.Equal(IpcProtocol.Version, 1);
    }

    private static IpcResponse Response(string fixture) =>
        IpcJson.ParseResponse(Encoding.UTF8.GetBytes(ContractFiles.WindowsFixture("ipc-action-error.json").GetProperty(fixture).GetRawText()));

    [Fact]
    public void Ok_true_with_an_action_error_is_not_a_success()
    {
        var response = Response("action_failed");
        Assert.True(response.Ok); // the transport succeeded...
        var reply = IpcJson.ParseSnapshotReply(response);
        Assert.False(reply.Succeeded); // ...but the action did not
        Assert.Equal("plan_expired", reply.ActionError!.Code);
        Assert.Equal("ready", reply.Snapshot.State);
        Assert.Equal(9_007_199_254_740_993, reply.Snapshot.Entitlement!.TotalBytes);
        Assert.Equal(long.MaxValue, reply.Snapshot.Entitlement.UsedUnits);
    }

    [Fact]
    public void Null_action_error_means_the_action_succeeded()
    {
        var reply = IpcJson.ParseSnapshotReply(Response("action_ok"));
        Assert.True(reply.Succeeded);
        Assert.Equal("connected", reply.Snapshot.State);
        Assert.Equal("up", reply.Snapshot.TunnelHealth);
    }

    [Fact]
    public void Transport_failure_throws_with_the_stable_code()
    {
        var error = Assert.Throws<IpcCommandException>(() => IpcJson.ParseSnapshotReply(Response("transport_failed")));
        Assert.Equal("unknown_command", error.Code);
    }

    [Fact]
    public void Ok_without_a_result_is_malformed()
    {
        var response = IpcJson.ParseResponse("""{"request_id":"x","protocol_version":1,"generation":1,"ok":true}"""u8);
        Assert.Throws<JsonException>(() => IpcJson.ParseSnapshotReply(response));
    }
}

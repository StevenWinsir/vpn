using System.Buffers.Binary;

namespace AsterLink.Shared.Ipc;

public enum IpcFrameError
{
    /// <summary>The peer closed the stream before any header byte (a clean end).</summary>
    EndOfStream,
    /// <summary>The header or body was cut short.</summary>
    Truncated,
    Empty,
    TooLarge,
}

public sealed class IpcFrameException(IpcFrameError error, string message) : IOException(message)
{
    public IpcFrameError Error { get; } = error;
}

/// <summary>
/// 4-byte big-endian length + one JSON document, exactly as ipc-v1.json and the Go
/// <c>ipc.ReadFrame/WriteFrame</c>. The body is never allocated before the declared
/// length has been checked against the limit.
/// </summary>
public static class IpcFrame
{
    public const int MaxFrame = 2 << 20;
    public const int HeaderSize = 4;

    public static async Task WriteAsync(Stream stream, ReadOnlyMemory<byte> body, CancellationToken cancellationToken = default)
    {
        if (body.IsEmpty) throw new IpcFrameException(IpcFrameError.Empty, "empty frame");
        if (body.Length > MaxFrame) throw new IpcFrameException(IpcFrameError.TooLarge, "frame exceeds limit");

        // One write call so a header can never be interleaved with another writer's body.
        var buffer = new byte[HeaderSize + body.Length];
        BinaryPrimitives.WriteUInt32BigEndian(buffer, (uint)body.Length);
        body.CopyTo(buffer.AsMemory(HeaderSize));
        await stream.WriteAsync(buffer, cancellationToken).ConfigureAwait(false);
        await stream.FlushAsync(cancellationToken).ConfigureAwait(false);
    }

    public static async Task<byte[]> ReadAsync(Stream stream, int limit = MaxFrame, CancellationToken cancellationToken = default)
    {
        if (limit <= 0 || limit > MaxFrame) limit = MaxFrame;

        var header = new byte[HeaderSize];
        var got = await ReadFullAsync(stream, header, cancellationToken).ConfigureAwait(false);
        if (got == 0) throw new IpcFrameException(IpcFrameError.EndOfStream, "end of stream");
        if (got < HeaderSize) throw new IpcFrameException(IpcFrameError.Truncated, "truncated header");

        var size = BinaryPrimitives.ReadUInt32BigEndian(header);
        if (size == 0) throw new IpcFrameException(IpcFrameError.Empty, "empty frame");
        if (size > (uint)limit) throw new IpcFrameException(IpcFrameError.TooLarge, $"frame {size} exceeds limit {limit}");

        var body = new byte[size];
        if (await ReadFullAsync(stream, body, cancellationToken).ConfigureAwait(false) < body.Length)
            throw new IpcFrameException(IpcFrameError.Truncated, "truncated body");
        return body;
    }

    private static async Task<int> ReadFullAsync(Stream stream, byte[] buffer, CancellationToken cancellationToken)
    {
        var total = 0;
        while (total < buffer.Length)
        {
            var read = await stream.ReadAsync(buffer.AsMemory(total), cancellationToken).ConfigureAwait(false);
            if (read == 0) break;
            total += read;
        }
        return total;
    }
}

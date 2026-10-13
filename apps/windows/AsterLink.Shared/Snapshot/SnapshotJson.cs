using System.Text.Json;
using System.Text.Json.Serialization;

namespace AsterLink.Shared.Snapshot;

public static class SnapshotJson
{
    // Strict numbers: a string or a fractional value where an integer is expected is an
    // error rather than a silent coercion, and values never pass through double.
    public static readonly JsonSerializerOptions Options = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        NumberHandling = JsonNumberHandling.Strict,
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
        ReadCommentHandling = JsonCommentHandling.Disallow,
        AllowTrailingCommas = false,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        MaxDepth = 16,
    };

    public static CoreSnapshot ParseSnapshot(string json) =>
        JsonSerializer.Deserialize<CoreSnapshot>(json, Options)
        ?? throw new JsonException("snapshot is null");

    public static CoreSnapshot ParseSnapshot(JsonElement element) =>
        element.Deserialize<CoreSnapshot>(Options)
        ?? throw new JsonException("snapshot is null");
}

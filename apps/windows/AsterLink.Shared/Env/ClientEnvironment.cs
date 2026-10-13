namespace AsterLink.Shared.Env;

public sealed class ClientEnvironmentException(string message) : FormatException(message);

/// <summary>
/// Strict parser for apps/windows/.env — public build parameters only. It mirrors
/// scripts/native_env.py and the Swift ClientEnvironment; all three are pinned to
/// shared/contracts/env-v1.json. This is a parser, never a shell `source`.
/// </summary>
public sealed record ClientEnvironment(string AppEnv, Uri ApiBaseUrl, Uri WebsiteBaseUrl)
{
    public const string ApiPath = "/api/v1/client";
    public const int MaxFileBytes = 8192;

    private static readonly string[] Allowed = ["APP_ENV", "API_BASE_URL", "WEBSITE_BASE_URL"];
    private const string UnsafeChars = "\"'$`\\;|&<>(){}*!";
    private static readonly HashSet<string> Loopback = new(StringComparer.OrdinalIgnoreCase) { "localhost", "127.0.0.1", "::1" };

    public bool IsRelease => AppEnv == "release";

    public static ClientEnvironment Parse(string text)
    {
        var values = new Dictionary<string, string>(StringComparer.Ordinal);
        var number = 0;
        foreach (var raw in text.Split('\n'))
        {
            number++;
            var line = raw.TrimEnd('\r');
            if (line.Length == 0 || line.StartsWith('#')) continue;

            var equals = line.IndexOf('=');
            if (equals < 0) throw new ClientEnvironmentException($"malformed line {number}");
            var key = line[..equals];
            var value = line[(equals + 1)..];

            if (!Allowed.Contains(key)) throw new ClientEnvironmentException($"unknown key {key}");
            if (values.ContainsKey(key)) throw new ClientEnvironmentException($"duplicate key {key}");
            if (!IsSafe(value)) throw new ClientEnvironmentException($"unsafe value for {key}");
            values[key] = value;
        }

        foreach (var key in Allowed)
            if (!values.ContainsKey(key)) throw new ClientEnvironmentException($"missing key {key}");

        var channel = values["APP_ENV"];
        if (channel is not ("development" or "release")) throw new ClientEnvironmentException("invalid APP_ENV");

        var api = ParseUrl(values["API_BASE_URL"], "API_BASE_URL", channel, ApiPath);
        var website = ParseUrl(values["WEBSITE_BASE_URL"], "WEBSITE_BASE_URL", channel, requiredPath: null);
        return new ClientEnvironment(channel, api, website);
    }

    public static ClientEnvironment Load(string path)
    {
        var info = new FileInfo(path);
        if (!info.Exists || info.LinkTarget is not null || (info.Attributes & FileAttributes.Directory) != 0)
            throw new ClientEnvironmentException("env file must be a regular, non-symlink file");
        if (info.Length > MaxFileBytes) throw new ClientEnvironmentException("env file too large");
        return Parse(File.ReadAllText(path));
    }

    private static bool IsSafe(string value)
    {
        if (value.Length == 0) return false;
        foreach (var c in value)
            if (c <= 0x20 || c >= 0x7f || UnsafeChars.Contains(c)) return false;
        return true;
    }

    private static Uri ParseUrl(string value, string key, string channel, string? requiredPath)
    {
        if (!Uri.TryCreate(value, UriKind.Absolute, out var uri) || string.IsNullOrEmpty(uri.Host))
            throw new ClientEnvironmentException($"invalid URL for {key}");
        // UserInfo, query and fragment are all refused: no credential or tracking material.
        if (uri.UserInfo.Length != 0 || uri.Query.Length != 0 || uri.Fragment.Length != 0
            || value.Contains('?') || value.Contains('#') || value.Contains('@'))
            throw new ClientEnvironmentException($"invalid URL for {key}");

        // Uri.Host keeps the brackets of an IPv6 literal; the contract compares the bare address.
        var host = uri.Host.Trim('[', ']');
        var https = uri.Scheme == Uri.UriSchemeHttps;
        var loopbackHttp = uri.Scheme == Uri.UriSchemeHttp && channel == "development" && Loopback.Contains(host);
        if (!https && !loopbackHttp) throw new ClientEnvironmentException($"invalid URL scheme/host for {key}");

        // AbsolutePath is percent-encoded the way it was written, so dot segments and
        // doubled slashes can't be normalised into the required path.
        var path = uri.AbsolutePath;
        if (requiredPath is not null)
        {
            if (path != requiredPath || value.Contains("..")) throw new ClientEnvironmentException($"invalid path for {key}");
        }
        else if (path is not ("" or "/"))
        {
            throw new ClientEnvironmentException($"invalid path for {key}");
        }
        return uri;
    }
}

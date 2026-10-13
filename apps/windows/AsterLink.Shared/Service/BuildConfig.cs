using System.Text;
using AsterLink.Shared.Env;

namespace AsterLink.Shared.Service;

/// <summary>
/// The public build parameters, frozen into the protected install directory. Editing a
/// .env next to a running product changes nothing (handoff 6.2): only a new build and
/// install does. The service reads this file, never anything the GUI offers.
/// </summary>
public static class BuildConfig
{
    public static ClientEnvironment Parse(ReadOnlySpan<byte> json)
    {
        System.Text.Json.JsonElement root;
        try { root = System.Text.Json.JsonDocument.Parse(json.ToArray()).RootElement; }
        catch (System.Text.Json.JsonException) { throw new ClientEnvironmentException("build config is not JSON"); }
        if (root.ValueKind != System.Text.Json.JsonValueKind.Object) throw new ClientEnvironmentException("build config must be an object");

        // Re-use the strict .env validation so a build config can never accept what a .env would refuse.
        var text = new StringBuilder();
        var seen = 0;
        foreach (var property in root.EnumerateObject())
        {
            if (property.Name is not ("APP_ENV" or "API_BASE_URL" or "WEBSITE_BASE_URL"))
                throw new ClientEnvironmentException("unknown build config key");
            if (property.Value.ValueKind != System.Text.Json.JsonValueKind.String)
                throw new ClientEnvironmentException("build config values must be strings");
            text.Append(property.Name).Append('=').Append(property.Value.GetString()).Append('\n');
            seen++;
        }
        if (seen != 3) throw new ClientEnvironmentException("build config must have exactly three keys");
        return ClientEnvironment.Parse(text.ToString());
    }

    public static byte[] Serialize(ClientEnvironment environment) =>
        System.Text.Json.JsonSerializer.SerializeToUtf8Bytes(new Dictionary<string, string>
        {
            ["APP_ENV"] = environment.AppEnv,
            ["API_BASE_URL"] = environment.ApiBaseUrl.AbsoluteUri.TrimEnd('/'),
            ["WEBSITE_BASE_URL"] = environment.WebsiteBaseUrl.AbsoluteUri.TrimEnd('/'),
        }, new System.Text.Json.JsonSerializerOptions { WriteIndented = true });
}

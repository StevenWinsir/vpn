using System.Text.Json;

namespace AsterLink.Tests;

internal static class ContractFiles
{
    public static JsonDocument Load(string relativePath)
    {
        var path = Path.Combine(AppContext.BaseDirectory, relativePath);
        return JsonDocument.Parse(File.ReadAllText(path));
    }

    public static JsonElement SharedContract(string name) => Load(Path.Combine("contracts", name)).RootElement;
    public static JsonElement WindowsFixture(string name) => Load(Path.Combine("Fixtures", name)).RootElement;
}

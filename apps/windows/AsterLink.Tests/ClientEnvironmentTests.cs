using System.Text.Json;
using AsterLink.Shared.Env;

namespace AsterLink.Tests;

public class ClientEnvironmentTests
{
    public static TheoryData<string> CaseNames => new(
        ContractFiles.SharedContract("env-v1.json").GetProperty("cases").EnumerateArray().Select(c => c.GetProperty("name").GetString()!));

    [Theory]
    [MemberData(nameof(CaseNames))]
    public void Matches_the_shared_env_contract(string name)
    {
        var testCase = ContractFiles.SharedContract("env-v1.json").GetProperty("cases").EnumerateArray()
            .Single(c => c.GetProperty("name").GetString() == name);
        var env = testCase.GetProperty("env").GetString()!;

        if (testCase.GetProperty("ok").GetBoolean())
            ClientEnvironment.Parse(env);
        else
            Assert.Throws<ClientEnvironmentException>(() => ClientEnvironment.Parse(env));
    }

    [Fact]
    public void Contract_is_not_empty()
    {
        Assert.True(ContractFiles.SharedContract("env-v1.json").GetProperty("cases").GetArrayLength() >= 10);
    }

    [Fact]
    public void Parsed_values_are_exposed()
    {
        var env = ClientEnvironment.Parse(
            "APP_ENV=release\nAPI_BASE_URL=https://demo.hyshentou.cn/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn/\n");
        Assert.True(env.IsRelease);
        Assert.Equal("demo.hyshentou.cn", env.ApiBaseUrl.Host);
        Assert.Equal("/api/v1/client", env.ApiBaseUrl.AbsolutePath);
        Assert.Equal("test.hyshentou.cn", env.WebsiteBaseUrl.Host);
    }

    [Fact]
    public void Ipv6_loopback_is_allowed_for_development_http()
    {
        ClientEnvironment.Parse("APP_ENV=development\nAPI_BASE_URL=http://[::1]:8080/api/v1/client\nWEBSITE_BASE_URL=http://localhost:3000\n");
    }

    [Theory]
    [InlineData("API_BASE_URL=https://demo.hyshentou.cn/api/v1/client/")]
    [InlineData("API_BASE_URL=https://demo.hyshentou.cn//api/v1/client")]
    [InlineData("API_BASE_URL=https://demo.hyshentou.cn/api/v1/x/../client")]
    [InlineData("API_BASE_URL=https://demo.hyshentou.cn/api/v1/client#frag")]
    [InlineData("API_BASE_URL=https://demo.hyshentou.cn/api/v1/client?")]
    [InlineData("API_BASE_URL=ftp://demo.hyshentou.cn/api/v1/client")]
    [InlineData("API_BASE_URL=demo.hyshentou.cn/api/v1/client")]
    [InlineData("API_BASE_URL=https://demo.hyshentou.cn /api/v1/client")]
    public void Malformed_api_urls_are_refused(string apiLine)
    {
        var env = $"APP_ENV=release\n{apiLine}\nWEBSITE_BASE_URL=https://test.hyshentou.cn\n";
        Assert.Throws<ClientEnvironmentException>(() => ClientEnvironment.Parse(env));
    }

    [Fact]
    public void Website_url_with_a_path_is_refused()
    {
        Assert.Throws<ClientEnvironmentException>(() => ClientEnvironment.Parse(
            "APP_ENV=release\nAPI_BASE_URL=https://demo.hyshentou.cn/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn/pay\n"));
    }

    [Fact]
    public void Crlf_line_endings_are_tolerated()
    {
        ClientEnvironment.Parse(
            "APP_ENV=release\r\nAPI_BASE_URL=https://demo.hyshentou.cn/api/v1/client\r\nWEBSITE_BASE_URL=https://test.hyshentou.cn\r\n");
    }

    [Fact]
    public void The_committed_example_file_is_valid()
    {
        var example = FindUpwards("apps/windows/.env.example");
        var env = ClientEnvironment.Load(example);
        Assert.Equal("development", env.AppEnv);
    }

    [Fact]
    public void Missing_oversized_and_directory_paths_are_refused()
    {
        Assert.Throws<ClientEnvironmentException>(() => ClientEnvironment.Load(Path.Combine(Path.GetTempPath(), Guid.NewGuid().ToString("N"))));
        Assert.Throws<ClientEnvironmentException>(() => ClientEnvironment.Load(Path.GetTempPath()));

        var big = Path.GetTempFileName();
        try
        {
            File.WriteAllText(big, new string('#', ClientEnvironment.MaxFileBytes + 1));
            Assert.Throws<ClientEnvironmentException>(() => ClientEnvironment.Load(big));
        }
        finally { File.Delete(big); }
    }

    [Fact]
    public void Symlinked_env_files_are_refused()
    {
        var directory = Directory.CreateTempSubdirectory("asterlink-env");
        try
        {
            var real = Path.Combine(directory.FullName, "real.env");
            File.WriteAllText(real, File.ReadAllText(FindUpwards("apps/windows/.env.example")));
            var link = Path.Combine(directory.FullName, "link.env");
            try { File.CreateSymbolicLink(link, real); }
            catch (Exception ex) when (ex is UnauthorizedAccessException or IOException) { return; } // symlinks need privilege on Windows
            Assert.Throws<ClientEnvironmentException>(() => ClientEnvironment.Load(link));
        }
        finally { directory.Delete(recursive: true); }
    }

    private static string FindUpwards(string relative)
    {
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            var candidate = Path.Combine(dir.FullName, relative);
            if (File.Exists(candidate)) return candidate;
        }
        throw new FileNotFoundException(relative);
    }
}

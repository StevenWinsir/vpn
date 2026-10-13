using System.Text;
using System.Text.Json;
using AsterLink.Shared.Env;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;

namespace AsterLink.Tests;

public class WinPathTests
{
    [Theory]
    [InlineData(@"C:\Program Files\AsterLink\core\asterlink-core.exe")]
    [InlineData(@"c:\x")]
    [InlineData(@"D:\")]
    public void Plain_drive_paths_are_accepted(string path) => Assert.True(WinPath.IsSafeAbsolute(path));

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData(@"core\asterlink-core.exe")]
    [InlineData(@"\\server\share\core.exe")]
    [InlineData(@"\\?\C:\Program Files\AsterLink\core.exe")]
    [InlineData(@"\\.\pipe\x")]
    [InlineData(@"C:\Program Files\AsterLink\..\Users\core.exe")]
    [InlineData(@"C:\Program Files\.\AsterLink")]
    [InlineData(@"C:\Program Files\AsterLink\core.exe:evil")]
    [InlineData(@"C:\Program Files\AsterLink\core.exe ")]
    [InlineData(@"C:\Program Files\AsterLink\core.")]
    [InlineData(@"C:\Program Files\\AsterLink")]
    [InlineData(@"C:/Program Files/AsterLink")]
    [InlineData(@"C:\Program Files\AsterLink\NUL")]
    [InlineData(@"C:\Program Files\AsterLink\con.txt")]
    [InlineData("C:\\Program Files\\Aster\u0001Link")]
    [InlineData(@"C:\Program Files\Aster*Link")]
    public void Dangerous_or_ambiguous_paths_are_refused(string? path) => Assert.False(WinPath.IsSafeAbsolute(path));

    [Fact]
    public void Comparison_ignores_case_and_a_trailing_separator_only()
    {
        Assert.True(WinPath.Equal(@"C:\Program Files\AsterLink\", @"c:\program files\asterlink"));
        Assert.False(WinPath.Equal(@"C:\Program Files\AsterLink", @"C:\Program Files\AsterLink2"));
        Assert.False(WinPath.Equal(null, @"C:\x"));
    }

    [Fact]
    public void Under_requires_a_real_path_boundary()
    {
        const string root = @"C:\Program Files\AsterLink";
        Assert.True(WinPath.IsUnder(root, root + @"\core\asterlink-core.exe"));
        Assert.False(WinPath.IsUnder(root, root));
        Assert.False(WinPath.IsUnder(root, @"C:\Program Files\AsterLink-evil\core.exe"));
        Assert.False(WinPath.IsUnder(root, @"C:\Users\x\AsterLink\core.exe"));
    }

    [Theory]
    [InlineData("core/asterlink-core.exe", true)]
    [InlineData("build-config.json", true)]
    [InlineData("../core.exe", false)]
    [InlineData("/core.exe", false)]
    [InlineData(@"core\x.exe", false)]
    [InlineData("core/x.exe:ads", false)]
    [InlineData("core//x.exe", false)]
    [InlineData("core/CON", false)]
    public void Manifest_paths_are_relative_and_forward_slashed(string path, bool expected) =>
        Assert.Equal(expected, WinPath.IsSafeRelative(path));
}

public class InstallLayoutTests
{
    [Fact]
    public void Layout_is_derived_from_a_component_directory()
    {
        var layout = InstallLayout.FromComponentDirectory(@"C:\Program Files\AsterLink\app\");
        Assert.NotNull(layout);
        Assert.Equal(@"C:\Program Files\AsterLink\core\asterlink-core.exe", layout.CoreExe);
        Assert.Equal(@"C:\Program Files\AsterLink\service\AsterLink.Service.exe", layout.ServiceExe);
        Assert.True(layout.IsAt(@"c:\program files\asterlink"));
    }

    [Theory]
    [InlineData(@"C:\Users\me\Downloads")]
    [InlineData(@"C:\Program Files\AsterLink")]
    [InlineData(@"C:\AsterLink\bin")]
    [InlineData(@"..\app")]
    public void Unknown_directories_give_no_layout(string directory) =>
        Assert.Null(InstallLayout.FromComponentDirectory(directory));
}

public class ManifestTests : IDisposable
{
    private readonly string root = Directory.CreateTempSubdirectory("asterlink-manifest-").FullName;

    public void Dispose() => Directory.Delete(root, recursive: true);

    private static string Hash(string text) => ReleaseManifest.HashStream(new MemoryStream(Encoding.UTF8.GetBytes(text)));

    private void Write(string relative, string text)
    {
        var path = Path.Combine(root, relative.Replace('/', Path.DirectorySeparatorChar));
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        File.WriteAllText(path, text);
    }

    private ReleaseManifest Build(Action<List<ManifestFile>>? tweak = null)
    {
        var files = new List<ManifestFile>
        {
            new() { Path = "app/AsterLink.exe", Sha256 = Hash("gui"), Size = 3, Role = "gui" },
            new() { Path = "app/AsterLink.dll", Sha256 = Hash("guicode"), Size = 7 },
            new() { Path = "service/AsterLink.Service.exe", Sha256 = Hash("svc"), Size = 3, Role = "service" },
            new() { Path = "core/asterlink-core.exe", Sha256 = Hash("core"), Size = 4, Role = "core" },
            new() { Path = "build-config.json", Sha256 = Hash("{}"), Size = 2, Role = "config" },
        };
        tweak?.Invoke(files);
        return new ReleaseManifest { FormatVersion = 1, Version = "0.1.0", Channel = "internal", ProtocolVersion = 1, Files = files };
    }

    private void Populate()
    {
        Write("app/AsterLink.exe", "gui");
        Write("app/AsterLink.dll", "guicode");
        Write("service/AsterLink.Service.exe", "svc");
        Write("core/asterlink-core.exe", "core");
        Write("build-config.json", "{}");
    }

    [Fact]
    public void A_manifest_round_trips_through_json()
    {
        var parsed = ReleaseManifest.Parse(Encoding.UTF8.GetBytes(Build().ToJson()));
        Assert.Equal("0.1.0", parsed.Version);
        Assert.Equal("core/asterlink-core.exe", parsed.Role("core").Path);
    }

    [Fact]
    public void An_intact_tree_verifies_clean()
    {
        Populate();
        Assert.Empty(ManifestVerifier.Verify(root, Build()));
    }

    [Fact]
    public void A_replaced_core_is_a_hash_mismatch()
    {
        Populate();
        Write("core/asterlink-core.exe", "evil!!");
        var problem = Assert.Single(ManifestVerifier.Verify(root, Build()));
        Assert.Equal(IntegrityProblemKind.SizeMismatch, problem.Kind);
        Write("core/asterlink-core.exe", "c0re");
        Assert.Equal(IntegrityProblemKind.HashMismatch, Assert.Single(ManifestVerifier.Verify(root, Build())).Kind);
    }

    [Fact]
    public void A_missing_file_and_a_planted_file_are_both_reported()
    {
        Populate();
        File.Delete(Path.Combine(root, "app", "AsterLink.dll"));
        Write("core/version.dll", "planted");
        var kinds = ManifestVerifier.Verify(root, Build()).Select(p => (p.Kind, p.Path)).ToList();
        Assert.Contains((IntegrityProblemKind.Missing, "app/AsterLink.dll"), kinds);
        Assert.Contains((IntegrityProblemKind.UnlistedFile, "core/version.dll"), kinds);
    }

    [Fact]
    public void A_symlinked_file_is_not_accepted_as_the_listed_file()
    {
        if (OperatingSystem.IsWindows()) return; // creating links needs a privilege there; covered by the Windows suite
        Populate();
        var real = Path.Combine(root, "elsewhere.bin");
        File.WriteAllText(real, "core");
        var link = Path.Combine(root, "core", "asterlink-core.exe");
        File.Delete(link);
        File.CreateSymbolicLink(link, real);
        Assert.Equal(IntegrityProblemKind.NotARegularFile, Assert.Single(ManifestVerifier.Verify(root, Build())).Kind);
    }

    [Fact]
    public void Role_files_must_sit_at_their_fixed_paths()
    {
        var wrongPath = Build(files => files[3] = new ManifestFile { Path = "core/other.exe", Sha256 = Hash("core"), Size = 4, Role = "core" });
        Assert.Throws<ManifestException>(wrongPath.Validate);
        var missingRole = Build(files => files.RemoveAt(3));
        Assert.Throws<ManifestException>(missingRole.Validate);
        var twice = Build(files => files.Add(new ManifestFile { Path = "core/b.exe", Sha256 = Hash("x"), Size = 1, Role = "core" }));
        Assert.Throws<ManifestException>(twice.Validate);
    }

    [Theory]
    [InlineData("../evil.dll")]
    [InlineData("other/dir.dll")]
    [InlineData("core/x.dll:ads")]
    public void Paths_outside_the_managed_tree_are_refused(string path)
    {
        var manifest = Build(files => files.Add(new ManifestFile { Path = path, Sha256 = Hash("x"), Size = 1 }));
        Assert.Throws<ManifestException>(manifest.Validate);
    }

    [Fact]
    public void Duplicates_bad_hashes_and_unknown_fields_are_refused()
    {
        Assert.Throws<ManifestException>(Build(f => f.Add(new ManifestFile { Path = "APP/asterlink.dll", Sha256 = Hash("x"), Size = 1 })).Validate);
        Assert.Throws<ManifestException>(Build(f => f[1] = new ManifestFile { Path = "app/AsterLink.dll", Sha256 = "ABC", Size = 7 }).Validate);
        Assert.Throws<ManifestException>(() => ReleaseManifest.Parse(Encoding.UTF8.GetBytes(
            Build().ToJson().Replace("\"version\"", "\"surprise\": 1,\n  \"version\""))));
        Assert.Throws<ManifestException>(() => ReleaseManifest.Parse("not json"u8));
    }

    [Fact]
    public void Channel_and_protocol_are_checked()
    {
        Assert.Throws<ManifestException>(new ReleaseManifest { FormatVersion = 1, Version = "0.1.0", Channel = "beta", ProtocolVersion = 1, Files = Build().Files }.Validate);
        Assert.Throws<ManifestException>(new ReleaseManifest { FormatVersion = 1, Version = "0.1.0", Channel = "internal", ProtocolVersion = 2, Files = Build().Files }.Validate);
        Assert.Throws<ManifestException>(new ReleaseManifest { FormatVersion = 1, Version = "latest", Channel = "internal", ProtocolVersion = 1, Files = Build().Files }.Validate);
    }
}

public class BuildConfigTests
{
    [Fact]
    public void Serialized_config_parses_back_to_the_same_environment()
    {
        var env = ClientEnvironment.Parse("APP_ENV=development\nAPI_BASE_URL=https://demo.hyshentou.cn/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn\n");
        var parsed = BuildConfig.Parse(BuildConfig.Serialize(env));
        Assert.Equal(env, parsed);
    }

    [Theory]
    [InlineData("""{"APP_ENV":"development","API_BASE_URL":"https://demo.hyshentou.cn/api/v1/client"}""")]
    [InlineData("""{"APP_ENV":"development","API_BASE_URL":"http://evil.example/api/v1/client","WEBSITE_BASE_URL":"https://test.hyshentou.cn"}""")]
    [InlineData("""{"APP_ENV":"development","API_BASE_URL":"https://demo.hyshentou.cn/api/v1/client","WEBSITE_BASE_URL":"https://test.hyshentou.cn","EXTRA":"1"}""")]
    [InlineData("""{"APP_ENV":"release","API_BASE_URL":"http://localhost/api/v1/client","WEBSITE_BASE_URL":"https://test.hyshentou.cn"}""")]
    [InlineData("""["x"]""")]
    [InlineData("nope")]
    public void Invalid_configs_are_refused(string json) =>
        Assert.Throws<ClientEnvironmentException>(() => BuildConfig.Parse(Encoding.UTF8.GetBytes(json)));
}

public class CommandPolicyTests
{
    private static readonly ClientEnvironment Env = ClientEnvironment.Parse(
        "APP_ENV=development\nAPI_BASE_URL=https://demo.hyshentou.cn/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn\n");

    private static CommandDecision Eval(string json) => CommandPolicy.Evaluate(Encoding.UTF8.GetBytes(json), Env);

    private static string Request(string command, string? payload = null, int protocol = 1) =>
        $$"""{"request_id":"r1","protocol_version":{{protocol}},"generation":3,"command":"{{command}}"{{(payload is null ? "" : ",\"payload\":" + payload)}}}""";

    [Fact]
    public void The_command_set_equals_the_shared_contract()
    {
        using var contract = JsonDocument.Parse(File.ReadAllText(Path.Combine(AppContext.BaseDirectory, "contracts", "ipc-v1.json")));
        var commands = contract.RootElement.GetProperty("commands").EnumerateArray().Select(e => e.GetString()!).Order().ToArray();
        Assert.Equal(commands, CommandPolicy.Limits.Keys.Order().ToArray());
    }

    [Theory]
    [InlineData("hello")] [InlineData("read_snapshot")] [InlineData("refresh_catalog")] [InlineData("disconnect")]
    [InlineData("logout")] [InlineData("shutdown")] [InlineData("connect")]
    public void Known_commands_pass_with_their_envelope(string command)
    {
        var decision = Eval(Request(command));
        Assert.True(decision.Allowed);
        Assert.Equal("r1", decision.Accepted!.RequestId);
        Assert.Equal(3UL, decision.Accepted.Generation);
    }

    [Theory]
    [InlineData("exec")] [InlineData("")] [InlineData("Hello")] [InlineData("hello ")] [InlineData("import_config")]
    public void Unknown_commands_are_refused_with_the_request_id_echoed(string command)
    {
        var decision = Eval(Request(command));
        Assert.False(decision.Allowed);
        Assert.Equal(ServiceErrors.UnknownCommand, decision.ErrorCode);
        Assert.Equal("r1", decision.RequestId);
    }

    [Theory]
    [InlineData("""{"request_id":"r","protocol_version":1,"generation":0,"command":"hello","path":"C:\\evil.exe"}""")]
    [InlineData("""{"request_id":"r","protocol_version":1,"generation":0,"command":"hello"}{}""")]
    [InlineData("""{"request_id":"r","request_id":"s","protocol_version":1,"generation":0,"command":"hello"}""")]
    [InlineData("""{"protocol_version":1,"generation":0,"command":"hello"}""")]
    [InlineData("""{"request_id":"","protocol_version":1,"generation":0,"command":"hello"}""")]
    [InlineData("""{"request_id":"r","protocol_version":1,"generation":-1,"command":"hello"}""")]
    [InlineData("""{"request_id":"r","protocol_version":"1","generation":0,"command":"hello"}""")]
    [InlineData("""{"request_id":"r","protocol_version":1,"generation":0,"command":7}""")]
    [InlineData("[]")]
    [InlineData("null")]
    [InlineData("{")]
    public void Malformed_requests_are_refused(string json)
    {
        var decision = Eval(json);
        Assert.False(decision.Allowed);
        Assert.Equal(ServiceErrors.InvalidRequest, decision.ErrorCode);
    }

    [Fact]
    public void A_request_id_over_64_bytes_is_refused() =>
        Assert.Equal(ServiceErrors.InvalidRequest, Eval($$"""{"request_id":"{{new string('a', 65)}}","protocol_version":1,"generation":0,"command":"hello"}""").ErrorCode);

    [Fact]
    public void The_protocol_version_is_checked_after_the_command_is_known()
    {
        Assert.Equal(ServiceErrors.ProtocolUnsupported, Eval(Request("hello", protocol: 2)).ErrorCode);
        Assert.Equal(ServiceErrors.UnknownCommand, Eval(Request("exec", protocol: 2)).ErrorCode);
    }

    [Fact]
    public void Each_command_has_its_own_size_ceiling()
    {
        var padded = Request("hello", "\"" + new string('a', 5000) + "\"");
        Assert.Equal(ServiceErrors.PayloadTooLarge, Eval(padded).ErrorCode);
        var login = Request("login", LoginPayload() .Replace("}", ",\"x\":\"" + new string('a', 100) + "\"}"));
        Assert.True(Encoding.UTF8.GetByteCount(login) < 16 << 10);
        Assert.True(CommandPolicy.MaxRequestFrame == 16 << 10);
    }

    private static string LoginPayload(string api = "https://demo.hyshentou.cn/api/v1/client", string channel = "development") =>
        $$"""{"email":"a@b.c","password":"pw","api_base":"{{api}}","channel":"{{channel}}","device_id":"d","app_version":"0.1.0"}""";

    [Fact]
    public void Login_against_the_configured_api_is_forwarded() =>
        Assert.True(Eval(Request("login", LoginPayload())).Allowed);

    [Fact]
    public void Login_with_a_trailing_slash_on_the_configured_api_is_still_that_api() =>
        Assert.True(Eval(Request("login", LoginPayload(api: "https://demo.hyshentou.cn/api/v1/client/"))).Allowed);

    [Theory]
    [InlineData("https://evil.example/api/v1/client", "development")]
    [InlineData("https://demo.hyshentou.cn/api/v1/client.evil.example", "development")]
    [InlineData("http://demo.hyshentou.cn/api/v1/client", "development")]
    [InlineData("https://DEMO.hyshentou.cn.evil.example/api/v1/client", "development")]
    [InlineData("https://demo.hyshentou.cn/api/v1/client", "release")]
    public void Login_to_any_other_target_is_stopped_before_the_password_reaches_the_core(string api, string channel)
    {
        var decision = Eval(Request("login", LoginPayload(api, channel)));
        Assert.False(decision.Allowed);
        Assert.Equal(ServiceErrors.LoginTargetMismatch, decision.ErrorCode);
    }

    [Fact]
    public void Login_without_a_target_is_stopped()
    {
        Assert.Equal(ServiceErrors.LoginTargetMismatch, Eval(Request("login")).ErrorCode);
        Assert.Equal(ServiceErrors.LoginTargetMismatch, Eval(Request("login", """{"email":"a","password":"b"}""")).ErrorCode);
    }

    [Fact]
    public void Service_replies_use_the_core_wire_shape()
    {
        var response = IpcJson.ParseResponse(ServiceReplies.Error("r9", 4, ServiceErrors.OwnerBusy, "busy"));
        Assert.False(response.Ok);
        Assert.Equal("r9", response.RequestId);
        Assert.Equal(4UL, response.Generation);
        Assert.Equal(ServiceErrors.OwnerBusy, response.Error!.Code);
        var ok = IpcJson.ParseResponse(ServiceReplies.Ok("r1", 0, new ServiceHelloResult { ServiceVersion = "0.1.0", Channel = "internal" }));
        Assert.True(ok.Ok);
        Assert.Equal("asterlink-service", ok.Result!.Value.GetProperty("component").GetString());
    }
}

public class PeerPolicyTests
{
    private const string Gui = @"C:\Program Files\AsterLink\app\AsterLink.exe";
    private const string Hash = "ab12";
    private const string User = "S-1-5-21-1-2-3-1001";

    private static PeerFacts Good() => new(1234, 2, 2, User, User, 0x2000, Gui, Hash);

    private static PeerDecision Check(PeerFacts facts) => PeerPolicy.Evaluate(facts, Gui, Hash);

    [Fact]
    public void The_installed_gui_as_an_ordinary_user_is_allowed() => Assert.True(Check(Good()).Allowed);

    [Fact]
    public void An_elevated_gui_is_allowed_too() => Assert.True(Check(Good() with { IntegrityRid = 0x3000 }).Allowed);

    [Fact]
    public void A_fake_gui_in_a_user_writable_folder_is_refused() =>
        Assert.Equal(PeerVerdict.ImagePath, Check(Good() with { ImagePath = @"C:\Users\me\Downloads\AsterLink.exe" }).Verdict);

    [Fact]
    public void A_look_alike_path_is_refused() =>
        Assert.Equal(PeerVerdict.ImagePath, Check(Good() with { ImagePath = @"C:\Program Files\AsterLink2\app\AsterLink.exe" }).Verdict);

    [Fact]
    public void The_right_path_with_a_different_binary_is_refused() =>
        Assert.Equal(PeerVerdict.ImageHash, Check(Good() with { ImageSha256 = "ffff" }).Verdict);

    [Fact]
    public void A_missing_hash_or_path_is_refused()
    {
        Assert.Equal(PeerVerdict.ImageHash, Check(Good() with { ImageSha256 = null }).Verdict);
        Assert.Equal(PeerVerdict.ImagePath, Check(Good() with { ImagePath = null }).Verdict);
    }

    [Fact]
    public void A_process_whose_owner_is_not_the_pipe_client_is_refused() =>
        Assert.Equal(PeerVerdict.UserMismatch, Check(Good() with { ImpersonatedUserSid = "S-1-5-21-1-2-3-1002" }).Verdict);

    [Theory]
    [InlineData("S-1-5-18")] [InlineData("S-1-5-19")] [InlineData("S-1-5-20")] [InlineData("S-1-5-7")] [InlineData("S-1-1-0")]
    public void Service_and_anonymous_accounts_are_refused(string sid) =>
        Assert.Equal(PeerVerdict.ServiceAccount, Check(Good() with { TokenUserSid = sid, ImpersonatedUserSid = sid }).Verdict);

    [Fact]
    public void Unknown_identity_is_refused()
    {
        Assert.Equal(PeerVerdict.UnknownIdentity, Check(Good() with { TokenUserSid = null }).Verdict);
        Assert.Equal(PeerVerdict.UnknownIdentity, Check(Good() with { ImpersonatedUserSid = "" }).Verdict);
    }

    [Fact]
    public void Session_zero_and_a_session_mismatch_are_refused()
    {
        Assert.Equal(PeerVerdict.SessionZero, Check(Good() with { PipeSessionId = 0, ProcessSessionId = 0 }).Verdict);
        Assert.Equal(PeerVerdict.SessionMismatch, Check(Good() with { ProcessSessionId = 3 }).Verdict);
    }

    [Theory]
    [InlineData(0x0000u)] [InlineData(0x1000u)]
    public void Low_integrity_callers_are_refused(uint rid) =>
        Assert.Equal(PeerVerdict.LowIntegrity, Check(Good() with { IntegrityRid = rid }).Verdict);
}

public class OwnerGateTests
{
    private sealed class Conn : IOwnerConnection
    {
        public bool Closed;
        public Task CloseAsync() { Closed = true; return Task.CompletedTask; }
    }

    [Fact]
    public void The_first_user_owns_the_slot_and_a_second_user_is_busy()
    {
        var gate = new OwnerGate();
        var first = new Conn();
        Assert.Equal(OwnerOutcome.Granted, gate.TryClaim("S-1", 2, first).Outcome);
        var second = gate.TryClaim("S-2", 3, new Conn());
        Assert.Equal(OwnerOutcome.Busy, second.Outcome);
        Assert.False(first.Closed);
    }

    [Fact]
    public void The_same_user_in_another_session_is_busy_too() =>
        Assert.Equal(OwnerOutcome.Busy, StartedBy("S-1", 2).TryClaim("S-1", 3, new Conn()).Outcome);

    private static OwnerGate StartedBy(string sid, int session)
    {
        var gate = new OwnerGate();
        gate.TryClaim(sid, session, new Conn());
        return gate;
    }

    [Fact]
    public void The_same_user_reconnecting_replaces_and_hands_back_the_old_connection()
    {
        var gate = new OwnerGate();
        var old = new Conn();
        gate.TryClaim("S-1", 2, old);
        var fresh = new Conn();
        var claim = gate.TryClaim("s-1", 2, fresh);
        Assert.Equal(OwnerOutcome.Granted, claim.Outcome);
        Assert.Same(old, claim.Previous);

        gate.Release(old); // the old connection finishing must not free the new owner's slot
        Assert.True(gate.IsOwned);
        Assert.Equal(OwnerOutcome.Busy, gate.TryClaim("S-2", 3, new Conn()).Outcome);
        gate.Release(fresh);
        Assert.False(gate.IsOwned);
        Assert.Equal(OwnerOutcome.Granted, gate.TryClaim("S-2", 3, new Conn()).Outcome);
    }
}

public class VersionGateTests
{
    [Fact]
    public void A_matched_set_is_compatible() => Assert.True(VersionGate.Check("0.1.0", "0.1.0", "0.1.0", "0.1.0").Compatible);

    [Fact]
    public void Core_version_is_optional_until_known() => Assert.True(VersionGate.Check("0.1.0", "0.1.0", "0.1.0", null).Compatible);

    [Theory]
    [InlineData("0.1.0", "0.0.9", "0.1.0")]
    [InlineData("0.0.9", "0.1.0", "0.1.0")]
    [InlineData("0.1.0", "0.1.0", "0.0.0-dev")]
    public void Any_older_or_newer_component_is_refused(string gui, string service, string core) =>
        Assert.False(VersionGate.Check("0.1.0", service, gui, core).Compatible);
}

public class AclPolicyTests
{
    private const string Users = "S-1-5-32-545";
    private const string System = "S-1-5-18";
    private const string Admins = "S-1-5-32-544";
    private const int ReadExecute = 0x1200A9;

    [Fact]
    public void Program_files_style_acl_is_safe() =>
        Assert.True(AclPolicy.Evaluate(Admins, [new(System, true, 0x1F01FF, false), new(Admins, true, 0x1F01FF, false), new(Users, true, ReadExecute, false)]).Safe);

    [Theory]
    [InlineData(AclPolicy.WriteData)]
    [InlineData(AclPolicy.AppendData)]
    [InlineData(AclPolicy.Delete)]
    [InlineData(AclPolicy.DeleteSubdirectoriesAndFiles)]
    [InlineData(AclPolicy.ChangePermissions)]
    [InlineData(AclPolicy.TakeOwnership)]
    [InlineData(AclPolicy.GenericAll)]
    [InlineData(AclPolicy.GenericWrite)]
    [InlineData(0x1301BF)] // the classic "Modify" mask
    public void A_standard_user_with_any_write_style_right_is_unsafe(int rights)
    {
        var verdict = AclPolicy.Evaluate(Admins, [new(System, true, 0x1F01FF, false), new("S-1-5-11", true, rights, false)]);
        Assert.False(verdict.Safe);
    }

    [Fact]
    public void Everyone_and_the_current_user_with_write_are_unsafe()
    {
        Assert.False(AclPolicy.Evaluate(Admins, [new("S-1-1-0", true, AclPolicy.WriteData, false)]).Safe);
        Assert.False(AclPolicy.Evaluate(Admins, [new("S-1-5-21-1-2-3-1001", true, 0x1F01FF, false)]).Safe);
    }

    [Fact]
    public void Deny_entries_do_not_count_as_grants() =>
        Assert.True(AclPolicy.Evaluate(Admins, [new(Users, false, AclPolicy.WriteData, false)]).Safe);

    [Fact]
    public void An_untrusted_owner_can_always_rewrite_the_acl() =>
        Assert.False(AclPolicy.Evaluate("S-1-5-21-1-2-3-1001", [new(System, true, 0x1F01FF, false)]).Safe);

    [Fact]
    public void Creator_owner_inherit_only_is_ignored_but_a_direct_creator_owner_grant_is_trusted() =>
        Assert.True(AclPolicy.Evaluate(Admins, [new("S-1-3-0", true, 0x1F01FF, true)]).Safe);
}

public class RecoveryPolicyTests
{
    private const string Core = @"C:\Program Files\AsterLink\core\asterlink-core.exe";
    private static SessionMarker Marker() => new() { ServiceProcessId = 10, CoreProcessId = 20, CoreStartFileTime = 133_000_000_000_000_000, CorePath = Core, OwnerSid = "S-1-5-21-1", StartedAt = DateTimeOffset.UnixEpoch };

    [Fact]
    public void Marker_round_trips_and_contains_no_credential_field()
    {
        var bytes = Marker().ToJson();
        var parsed = SessionMarker.TryParse(bytes);
        Assert.NotNull(parsed);
        Assert.Equal(Marker().CoreStartFileTime, parsed.CoreStartFileTime);
        var text = Encoding.UTF8.GetString(bytes).ToLowerInvariant();
        foreach (var forbidden in new[] { "password", "token", "yaml", "secret" }) Assert.DoesNotContain(forbidden, text);
    }

    [Theory]
    [InlineData("")]
    [InlineData("{")]
    [InlineData("{}")]
    [InlineData("""{"format_version":1,"core_process_id":0,"core_start_file_time":1,"core_path":"C:\\x.exe"}""")]
    [InlineData("""{"format_version":1,"core_process_id":5,"core_start_file_time":1,"core_path":"..\\x.exe"}""")]
    [InlineData("""{"format_version":2,"core_process_id":5,"core_start_file_time":1,"core_path":"C:\\x.exe"}""")]
    [InlineData("""{"format_version":1,"core_process_id":5,"core_start_file_time":1,"core_path":"C:\\x.exe","surprise":1}""")]
    public void Anything_but_a_well_formed_marker_parses_to_null(string json) =>
        Assert.Null(SessionMarker.TryParse(Encoding.UTF8.GetBytes(json)));

    [Fact]
    public void No_file_means_nothing_to_do() => Assert.Equal(RecoveryAction.Nothing, RecoveryPolicy.Decide(false, null, null));

    [Fact]
    public void A_corrupt_marker_blocks_instead_of_guessing() => Assert.Equal(RecoveryAction.Block, RecoveryPolicy.Decide(true, null, null));

    [Fact]
    public void A_dead_core_only_clears_the_record() => Assert.Equal(RecoveryAction.ClearOnly, RecoveryPolicy.Decide(true, Marker(), null));

    [Fact]
    public void A_surviving_core_with_matching_start_time_and_image_is_killed() =>
        Assert.Equal(RecoveryAction.KillThenClear, RecoveryPolicy.Decide(true, Marker(), new RunningProcess(133_000_000_000_000_000, Core.ToUpperInvariant())));

    [Fact]
    public void A_reused_pid_is_not_ours_to_kill()
    {
        Assert.Equal(RecoveryAction.ClearOnly, RecoveryPolicy.Decide(true, Marker(), new RunningProcess(133_000_000_000_000_001, Core)));
        Assert.Equal(RecoveryAction.ClearOnly, RecoveryPolicy.Decide(true, Marker(), new RunningProcess(133_000_000_000_000_000, @"C:\Windows\notepad.exe")));
        Assert.Equal(RecoveryAction.ClearOnly, RecoveryPolicy.Decide(true, Marker(), new RunningProcess(133_000_000_000_000_000, null)));
    }
}

public class ServiceHealthTests
{
    [Fact]
    public void Not_installed_offers_install_only_when_the_gui_sits_next_to_a_payload()
    {
        var stage = ServiceHealthClassifier.Classify(false, true, ScmState.NotInstalled, null);
        Assert.Equal((ServiceHealthState.NotInstalled, RecommendedAction.InstallFromStage), (stage.State, stage.Action));
        var loose = ServiceHealthClassifier.Classify(false, false, ScmState.NotInstalled, null);
        Assert.Equal(RecommendedAction.None, loose.Action);
    }

    [Fact]
    public void A_gui_outside_the_install_root_is_sent_to_the_installed_copy() =>
        Assert.Equal(RecommendedAction.LaunchInstalledApp, ServiceHealthClassifier.Classify(false, true, ScmState.Running, null).Action);

    [Fact]
    public void A_stopped_service_offers_a_start() =>
        Assert.Equal(RecommendedAction.StartService, ServiceHealthClassifier.Classify(true, false, ScmState.Stopped, null).Action);

    [Theory]
    [InlineData(ServiceErrors.IntegrityFailed)]
    [InlineData(ServiceErrors.VersionMismatch)]
    [InlineData(ServiceErrors.RecoveryBlocked)]
    public void Integrity_style_errors_need_repair(string code)
    {
        var health = ServiceHealthClassifier.Classify(true, false, ScmState.Running, code);
        Assert.Equal((ServiceHealthState.NeedsRepair, RecommendedAction.Repair), (health.State, health.Action));
    }

    [Fact]
    public void A_running_service_without_errors_is_ready() =>
        Assert.Equal(ServiceHealthState.Ready, ServiceHealthClassifier.Classify(true, false, ScmState.Running, null).State);

    [Fact]
    public void A_busy_owner_is_not_something_a_repair_fixes() =>
        Assert.Equal(RecommendedAction.None, ServiceHealthClassifier.Classify(true, false, ScmState.Running, ServiceErrors.OwnerBusy).Action);

    [Fact]
    public void Every_message_is_non_empty_for_every_combination()
    {
        foreach (var scm in Enum.GetValues<ScmState>())
        foreach (var code in new string?[] { null, ServiceErrors.PeerRejected, ServiceErrors.WorkerUnavailable, "something_else" })
        foreach (var installed in new[] { true, false })
            Assert.False(string.IsNullOrWhiteSpace(ServiceHealthClassifier.Classify(installed, !installed, scm, code).Message));
    }
}

public class ServerPolicyTests
{
    private const string Exe = @"C:\Program Files\AsterLink\service\AsterLink.Service.exe";

    private static ServerFacts Good() => new(1234, ScmState.Running, 1234, "\"" + Exe + "\"", "LocalSystem", true, Exe, "S-1-5-18");

    [Fact]
    public void The_registered_service_serving_the_pipe_is_accepted() => Assert.Null(ServerPolicy.Evaluate(Good(), Exe));

    [Fact]
    public void A_standard_user_that_cannot_open_the_system_process_is_still_accepted_on_the_scm_binding() =>
        Assert.Null(ServerPolicy.Evaluate(Good() with { ProcessInspectable = false, ImagePath = null, UserSid = null }, Exe));

    [Fact]
    public void A_server_that_is_not_the_scm_process_is_rejected_whether_or_not_it_can_be_inspected()
    {
        Assert.NotNull(ServerPolicy.Evaluate(Good() with { PipeServerProcessId = 999 }, Exe));
        Assert.NotNull(ServerPolicy.Evaluate(Good() with { PipeServerProcessId = 999, ProcessInspectable = false, ImagePath = null, UserSid = null }, Exe));
        Assert.NotNull(ServerPolicy.Evaluate(Good() with { PipeServerProcessId = 0 }, Exe));
    }

    [Fact]
    public void A_stopped_or_missing_service_is_rejected()
    {
        Assert.NotNull(ServerPolicy.Evaluate(Good() with { ScmState = ScmState.Stopped }, Exe));
        Assert.NotNull(ServerPolicy.Evaluate(Good() with { ScmState = ScmState.NotInstalled }, Exe));
    }

    [Theory]
    [InlineData(@"""C:\Users\me\AsterLink.Service.exe""")]
    [InlineData(@"C:\Program Files\AsterLink\service\AsterLink.Service.exe")]
    [InlineData(@"""C:\Program Files\AsterLink\service\AsterLink.Service.exe"" --evil")]
    [InlineData(null)]
    public void A_registration_that_does_not_point_at_the_protected_binary_is_rejected(string? path) =>
        Assert.NotNull(ServerPolicy.Evaluate(Good() with { ScmBinaryPath = path }, Exe));

    [Fact]
    public void A_non_system_account_is_rejected() =>
        Assert.NotNull(ServerPolicy.Evaluate(Good() with { ScmStartName = @".\someone" }, Exe));

    [Fact]
    public void When_the_process_can_be_inspected_it_must_be_the_protected_binary_running_as_system()
    {
        Assert.NotNull(ServerPolicy.Evaluate(Good() with { ImagePath = @"C:\Temp\evil.exe" }, Exe));
        Assert.NotNull(ServerPolicy.Evaluate(Good() with { UserSid = "S-1-5-21-1-2-3-1001" }, Exe));
    }
}

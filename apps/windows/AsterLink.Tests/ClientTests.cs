using System.Text.Json;
using AsterLink.Shared.Client;
using AsterLink.Shared.Ipc;
using AsterLink.Shared.Service;
using AsterLink.Shared.Snapshot;

namespace AsterLink.Tests;

public class FormattingTests
{
    [Theory]
    [InlineData(500, "0.5×")]
    [InlineData(1000, "1×")]
    [InlineData(1500, "1.5×")]
    [InlineData(1250, "1.25×")]
    [InlineData(2000, "2×")]
    [InlineData(100, "0.1×")]
    [InlineData(1, "0.001×")]
    public void Rate_is_exact_integer_arithmetic(long permille, string expected) => Assert.Equal(expected, Formatting.Rate(permille));

    [Theory]
    [InlineData(0, "0 B")]
    [InlineData(1023, "1023 B")]
    [InlineData(1024, "1.00 KiB")]
    [InlineData(104_857_600, "100.00 MiB")]
    [InlineData(1L << 30, "1.00 GiB")]
    [InlineData(-5, "0 B")]
    public void Bytes_use_binary_units(long value, string expected) => Assert.Equal(expected, Formatting.Bytes(value));

    [Fact]
    public void Values_above_2_pow_53_do_not_throw_or_go_negative()
    {
        Assert.StartsWith("8", Formatting.Bytes(long.MaxValue / 1024 / 1024 / 1024 / 1024 / 1024));
        Assert.EndsWith("TiB", Formatting.Bytes(long.MaxValue));
        Assert.Equal(1.0, Formatting.UsedFraction(9007199254740993, 0));
    }

    [Fact]
    public void Days_left_rounds_up_and_floors_at_zero()
    {
        var now = new DateTimeOffset(2030, 1, 1, 12, 0, 0, TimeSpan.Zero);
        Assert.Equal(1, Formatting.DaysLeft(now.AddHours(1), now));
        Assert.Equal(2, Formatting.DaysLeft(now.AddHours(25), now));
        Assert.Equal(0, Formatting.DaysLeft(now.AddSeconds(-1), now));
        Assert.Null(Formatting.DaysLeft(null, now));
    }

    [Fact]
    public void Used_fraction_is_clamped_and_safe_with_a_zero_total()
    {
        Assert.Equal(0, Formatting.UsedFraction(0, 0));
        Assert.Equal(0.25, Formatting.UsedFraction(100, 75));
        Assert.Equal(1, Formatting.UsedFraction(100, -50));
        Assert.Equal(0, Formatting.UsedFraction(100, 500));
    }
}

public class LoginInputTests
{
    [Theory]
    [InlineData("", "x")]
    [InlineData("   ", "x")]
    [InlineData("a@b.c", "")]
    public void Missing_input_is_refused(string email, string password) => Assert.NotNull(LoginInput.Validate(email, password));

    [Fact]
    public void Limits_match_the_core()
    {
        Assert.Null(LoginInput.Validate("a@b.c", new string('p', 72)));
        Assert.NotNull(LoginInput.Validate("a@b.c", new string('p', 73)));
        // 72 bytes, not 72 characters: 25 three-byte characters are 75 bytes.
        Assert.NotNull(LoginInput.Validate("a@b.c", new string('密', 25)));
        Assert.Null(LoginInput.Validate("a@b.c", new string('密', 24)));
        Assert.NotNull(LoginInput.Validate(new string('a', 250) + "@b.cc", "x"));
    }
}

public class CredentialAccountTests
{
    [Fact]
    public void Key_is_scoped_to_api_base_and_normalised_email()
    {
        var a = new Uri("https://api.example.test/api/v1/client");
        var b = new Uri("https://other.example.test/api/v1/client");
        Assert.Equal(CredentialAccount.Key(a, "User@Example.com "), CredentialAccount.Key(a, "user@example.com"));
        Assert.NotEqual(CredentialAccount.Key(a, "user@example.com"), CredentialAccount.Key(b, "user@example.com"));
        Assert.StartsWith("AsterLink/", CredentialAccount.TargetName(CredentialAccount.Key(a, "u@e.c")));
    }

    [Fact]
    public void Trailing_slash_on_the_api_base_does_not_make_a_second_account()
    {
        Assert.Equal(
            CredentialAccount.Key(new Uri("https://api.example.test/api/v1/client"), "u@e.c"),
            CredentialAccount.Key(new Uri("https://api.example.test/api/v1/client/"), "u@e.c"));
    }
}

public class LocalStateTests : IDisposable
{
    private readonly string directory = Path.Combine(Path.GetTempPath(), "asterlink-test-" + Guid.NewGuid().ToString("N"));

    public void Dispose()
    {
        if (Directory.Exists(directory)) Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public void Device_id_is_a_valid_uuid_and_stays_the_same()
    {
        var first = new FileDeviceIdentity(directory).DeviceId();
        Assert.True(DeviceIds.IsValid(first));
        Assert.Equal(first, new FileDeviceIdentity(directory).DeviceId());
    }

    [Fact]
    public void Corrupt_device_id_is_replaced_not_fatal()
    {
        Directory.CreateDirectory(directory);
        File.WriteAllText(Path.Combine(directory, FileDeviceIdentity.FileName), "not-a-uuid");
        var id = new FileDeviceIdentity(directory).DeviceId();
        Assert.True(DeviceIds.IsValid(id));
        Assert.Equal(id, File.ReadAllText(Path.Combine(directory, FileDeviceIdentity.FileName)).Trim());
    }

    [Fact]
    public void Two_installs_get_different_device_ids()
    {
        Assert.NotEqual(new FileDeviceIdentity(directory).DeviceId(), new FileDeviceIdentity(directory + "-b").DeviceId());
        Directory.Delete(directory + "-b", recursive: true);
    }

    [Fact]
    public void Preferences_round_trip_and_never_hold_a_password()
    {
        var prefs = new FileLoginPreferences(directory) { RememberedEmail = "user@example.com", Theme = "dark", TrayNoticeShown = true };
        var again = new FileLoginPreferences(directory);
        Assert.Equal("user@example.com", again.RememberedEmail);
        Assert.Equal("dark", again.Theme);
        Assert.True(again.TrayNoticeShown);
        Assert.DoesNotContain("password", File.ReadAllText(Path.Combine(directory, FileLoginPreferences.FileName)), StringComparison.OrdinalIgnoreCase);
        prefs.RememberedEmail = null;
        Assert.Null(new FileLoginPreferences(directory).RememberedEmail);
    }

    [Fact]
    public void Corrupt_or_unknown_preferences_fall_back_to_defaults()
    {
        Directory.CreateDirectory(directory);
        File.WriteAllText(Path.Combine(directory, FileLoginPreferences.FileName), "{{{ not json");
        var prefs = new FileLoginPreferences(directory);
        Assert.Null(prefs.RememberedEmail);
        Assert.Equal("system", prefs.Theme);
        prefs.Theme = "neon";
        Assert.Equal("system", prefs.Theme);
    }
}

public class PresentationTests
{
    [Fact]
    public void Admin_without_a_plan_cannot_connect()
    {
        var snapshot = Replies.Plan(authorized: false, selected: Replies.NodeA, configReady: true, role: "admin", reason: "paid_vip_required");
        var view = ConnectionView.From(snapshot, operationBusy: false);
        Assert.False(view.CanConnect);
        Assert.True(view.ShowConnect);
        Assert.Contains("套餐", view.DisabledReason);
    }

    [Fact]
    public void A_plan_without_a_selected_line_asks_for_one()
    {
        var view = ConnectionView.From(Replies.Plan(), false);
        Assert.False(view.CanConnect);
        Assert.Contains("选择", view.DisabledReason);
    }

    [Fact]
    public void No_nodes_is_its_own_empty_state()
    {
        var snapshot = Replies.Plan(nodes: []);
        Assert.Contains("尚未配置", ConnectionView.From(snapshot, false).DisabledReason);
        Assert.Contains("尚未配置", NodeList.EmptyText(snapshot, filtered: false));
    }

    [Fact]
    public void A_ready_selected_line_with_a_plan_can_connect_unless_busy()
    {
        var snapshot = Replies.Plan(selected: Replies.NodeA, configReady: true);
        Assert.True(ConnectionView.From(snapshot, false).CanConnect);
        Assert.False(ConnectionView.From(snapshot, true).CanConnect);
    }

    [Theory]
    [InlineData("connected", "已连接", true, false)]
    [InlineData("connecting", "正在连接…", false, false)]
    [InlineData("preparing", "正在准备…", false, false)]
    [InlineData("stopping", "正在断开…", false, false)]
    public void State_text_and_buttons_follow_the_snapshot(string state, string text, bool disconnect, bool connect)
    {
        var view = ConnectionView.From(Replies.Plan(state: state, selected: Replies.NodeA, configReady: true), false);
        Assert.Equal(text, view.StatusText);
        Assert.Equal(disconnect, view.ShowDisconnect);
        Assert.Equal(connect, view.ShowConnect);
        Assert.Equal(state != "connected", view.ShowProgress);
    }

    [Fact]
    public void A_blocked_state_shows_the_core_error_text()
    {
        var snapshot = Replies.Plan(state: "blocked", lastError: new ErrorView { Code = "quota_exhausted", Message = "套餐流量已用完" });
        var view = ConnectionView.From(snapshot, false);
        Assert.Equal("不可用", view.StatusText);
        Assert.Equal(Tone.Bad, view.Tone);
        Assert.Equal("套餐流量已用完", view.Detail);
    }

    [Theory]
    [InlineData("node_reported", "计量：节点实测，由服务器强制执行")]
    [InlineData("", "计量：客户端上报（内部测试线路）")]
    [InlineData("client_reported", "计量：客户端上报（内部测试线路）")]
    public void A_line_says_who_keeps_its_books(string source, string label)
    {
        Assert.Equal(label, NodeRow.MeteringLabelOf(source));
    }

    [Fact]
    public void Node_search_and_filter_use_only_visible_metadata_and_keep_server_order()
    {
        var snapshot = Replies.Plan(selected: Replies.NodeB, configReady: true);
        Assert.Equal(["东京专线", "香港普通"], NodeList.Build(snapshot, "", LineFilter.All).Select(r => r.Name));
        Assert.Equal(["东京专线"], NodeList.Build(snapshot, "", LineFilter.Dedicated).Select(r => r.Name));
        Assert.Equal(["香港普通"], NodeList.Build(snapshot, "", LineFilter.Direct).Select(r => r.Name));
        Assert.Equal(["香港普通"], NodeList.Build(snapshot, "香港", LineFilter.All).Select(r => r.Name));
        Assert.Equal(["东京专线"], NodeList.Build(snapshot, "日本", LineFilter.All).Select(r => r.Name));
        Assert.Equal(["东京专线"], NodeList.Build(snapshot, "专线", LineFilter.All).Select(r => r.Name));
        Assert.Empty(NodeList.Build(snapshot, "不存在", LineFilter.All));
        Assert.Contains("没有符合", NodeList.EmptyText(snapshot, filtered: true));
    }

    [Fact]
    public void Node_rows_show_the_admin_rate_and_never_hardcode_one_per_line_type()
    {
        var nodes = new[]
        {
            new NodeView { Id = Replies.NodeA, Name = "专线A", LineType = "dedicated", RatePermille = 2500 },
            new NodeView { Id = Replies.NodeB, Name = "普通B", LineType = "direct", RatePermille = 1000 },
        };
        var rows = NodeList.Build(Replies.Plan(nodes: nodes, selected: Replies.NodeA, configReady: true), "", LineFilter.All);
        Assert.Equal("2.5×", rows[0].RateLabel);
        Assert.Equal("1×", rows[1].RateLabel);
        Assert.True(rows[0].Selected);
        Assert.True(rows[0].ConfigVerified);
        Assert.False(rows[1].Selected);
        Assert.Equal("专线", rows[0].LineLabel);
        Assert.Equal("普通线路", rows[1].LineLabel);
    }

    [Fact]
    public void Node_rows_carry_no_configuration_fields()
    {
        var names = typeof(NodeRow).GetProperties().Select(p => p.Name.ToLowerInvariant());
        Assert.DoesNotContain(names, n => n.Contains("yaml") || n.Contains("server") || n.Contains("port") || n.Contains("password") || n.Contains("uuid") || n.Contains("secret"));
    }

    [Theory]
    [InlineData(ServiceErrors.ServiceNotInstalled, "安装")]
    [InlineData(ServiceErrors.ServiceNotRunning, "启动")]
    [InlineData(ServiceErrors.OwnerBusy, "另一位")]
    [InlineData(ServiceErrors.WorkerExited, "重新登录")]
    public void Service_errors_have_specific_text(string code, string fragment) => Assert.Contains(fragment, ErrorText.ForService(code));

    // Node groups round 6: online-presence refusals arrive at connect time in the Core's words; the
    // account can try again (another device may disconnect, another line may have room).
    [Theory]
    [InlineData("device_limit", "已达到同时在线设备上限，请先断开其他设备")]
    [InlineData("group_full", "该线路当前已满，请稍后再试或选择其他线路")]
    [InlineData("group_unavailable", "该线路暂无可用服务器，请选择其他线路")]
    [InlineData("connection_expired", "在线状态已过期，请重新连接")]
    public void An_online_presence_refusal_shows_the_core_text_and_allows_a_retry(string code, string message)
    {
        var view = ConnectionView.From(Replies.Plan(selected: Replies.NodeA, configReady: true, lastError: new ErrorView { Code = code, Message = message }), false);
        Assert.Equal(message, view.Detail);
        Assert.True(view.ShowConnect);
        Assert.True(view.CanConnect);
    }

    // connection_reassigned: while the Core moves to another server of the line by itself the
    // page shows progress, never a Connect button.
    [Fact]
    public void A_server_side_move_shows_progress_and_no_connect_button()
    {
        var moving = Replies.Plan(state: "preparing", selected: Replies.NodeA, configReady: true,
            lastError: new ErrorView { Code = "connection_reassigned", Message = "线路服务器已调整，正在为你重新连接" });
        var view = ConnectionView.From(moving, false);
        Assert.Equal("正在准备…", view.StatusText);
        Assert.False(view.ShowConnect);
        Assert.True(view.ShowProgress);
    }

    [Fact]
    public void Diagnostics_hold_state_but_no_secret_shaped_fields()
    {
        var snapshot = Replies.Plan(selected: Replies.NodeA, configReady: true, lastError: new ErrorView { Code = "tunnel_failed", Message = "x" });
        var text = DiagnosticsReport.Build("0.1.0", "development", "api.example.test", "Windows 10", snapshot, "特权服务就绪");
        Assert.Contains("state ready", text);
        Assert.Contains("last_error tunnel_failed", text);
        foreach (var forbidden in new[] { "password", "token", "yaml", Replies.NodeA, "user@example.com" })
            Assert.DoesNotContain(forbidden, text, StringComparison.OrdinalIgnoreCase);
    }
}

public class CoreClientTests
{
    [Fact]
    public async Task Login_payload_carries_the_pinned_target_and_nothing_else()
    {
        var harness = new Harness();
        harness.Script(Replies.Plan());
        var client = new CoreClient(harness.Transport, "https://api.example.test/api/v1/client", "release", "1.2.3");
        await client.LoginAsync("u@e.c", "secret", "dev-id");

        var payload = harness.Transport.Commands(IpcCommands.Login).Single().Payload!.Value;
        Assert.Equal(["api_base", "app_version", "channel", "device_id", "email", "password"],
            payload.EnumerateObject().Select(p => p.Name).Order(StringComparer.Ordinal));
        Assert.Equal("release", payload.GetProperty("channel").GetString());
        Assert.Equal("1.2.3", payload.GetProperty("app_version").GetString());
    }

    [Fact]
    public async Task Commands_that_act_on_the_session_quote_its_generation()
    {
        var harness = new Harness();
        harness.Script(Replies.Plan());
        var client = new CoreClient(harness.Transport, "https://api.example.test/api/v1/client", "development", "0.1.0");
        await client.SelectNodeAsync(7, Replies.NodeA, new string('b', 64));
        await client.ConnectAsync(7);
        await client.RefreshCatalogAsync(7);
        Assert.All(new[] { IpcCommands.SelectNode, IpcCommands.Connect, IpcCommands.RefreshCatalog },
            command => Assert.Equal(7UL, harness.Transport.Commands(command).Single().Generation));
        var select = harness.Transport.Commands(IpcCommands.SelectNode).Single().Payload!.Value;
        Assert.Equal(Replies.NodeA, select.GetProperty("node_id").GetString());
    }

    [Fact]
    public async Task A_business_failure_is_returned_in_the_reply_not_thrown()
    {
        var harness = new Harness();
        harness.Transport.Handler = (_, _, _) => Task.FromResult(Replies.Snapshot(Replies.Plan(), new ErrorView { Code = "quota_exhausted", Message = "套餐流量已用完" }));
        var client = new CoreClient(harness.Transport, "https://api.example.test/api/v1/client", "development", "0.1.0");
        var reply = await client.ConnectAsync(1);
        Assert.False(reply.Succeeded);
        Assert.Equal("quota_exhausted", reply.ActionError!.Code);
    }

    [Fact]
    public async Task A_refused_command_throws_with_the_code()
    {
        var harness = new Harness();
        harness.Transport.Handler = (_, _, _) => Task.FromResult(Replies.Refused("stale_generation", "状态已变化"));
        var client = new CoreClient(harness.Transport, "https://api.example.test/api/v1/client", "development", "0.1.0");
        var error = await Assert.ThrowsAsync<IpcCommandException>(() => client.ConnectAsync(1));
        Assert.Equal("stale_generation", error.Code);
    }
}

public class SessionControllerTests
{
    private static async Task<Harness> SignedIn(CoreSnapshot? snapshot = null, bool remember = false)
    {
        var harness = new Harness();
        harness.Script(snapshot ?? Replies.Plan());
        await harness.Session.LaunchAsync();
        await harness.Session.SignInAsync("user@example.com", "pw", remember);
        return harness;
    }

    [Fact]
    public async Task Wrong_password_stays_signed_out_with_the_core_message_and_forgets_a_stale_saved_password()
    {
        var harness = new Harness();
        harness.Credentials.Items[harness.Key("user@example.com")] = "old";
        harness.Script(Replies.SignedOut(), loginError: new ErrorView { Code = "invalid_credentials", Message = "邮箱或密码不正确" });
        await harness.Session.LaunchAsync(); // no remembered email yet: nothing automatic
        await harness.Session.SignInAsync("user@example.com", "old", remember: true);

        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Equal("邮箱或密码不正确", harness.Session.Message);
        Assert.Empty(harness.Credentials.Items);
        Assert.Null(harness.Preferences.RememberedEmail);
    }

    [Fact]
    public async Task Remember_saves_the_password_in_the_vault_and_the_email_in_preferences()
    {
        var harness = await SignedIn(remember: true);
        Assert.Equal(AppPhase.SignedIn, harness.Session.Phase);
        Assert.Equal("pw", harness.Credentials.Items[harness.Key("user@example.com")]);
        Assert.Equal("user@example.com", harness.Preferences.RememberedEmail);
        Assert.True(harness.Session.RememberPassword);
    }

    [Fact]
    public async Task Declining_to_remember_deletes_any_earlier_saved_password()
    {
        var harness = new Harness();
        harness.Credentials.Items[harness.Key("user@example.com")] = "old";
        harness.Preferences.RememberedEmail = "user@example.com";
        harness.Script(Replies.Plan());
        await harness.Session.LaunchAsync(); // signs in automatically with "old"
        await harness.Session.SignOutAsync();
        harness.Credentials.Items[harness.Key("user@example.com")] = "old";
        await harness.Session.SignInAsync("user@example.com", "pw", remember: false);
        Assert.Empty(harness.Credentials.Items);
        Assert.Null(harness.Preferences.RememberedEmail);
    }

    [Fact]
    public async Task Vault_failure_warns_and_never_falls_back_to_a_file()
    {
        var harness = new Harness();
        harness.Credentials.FailSave = true;
        harness.Script(Replies.Plan());
        await harness.Session.LaunchAsync();
        await harness.Session.SignInAsync("user@example.com", "pw", remember: true);

        Assert.Equal(AppPhase.SignedIn, harness.Session.Phase); // sign-in itself is not blocked
        Assert.Contains("凭据管理器", harness.Session.Warning);
        Assert.False(harness.Session.RememberPassword);
        Assert.Empty(harness.Credentials.Items);
        Assert.Null(harness.Preferences.RememberedEmail);
    }

    [Fact]
    public async Task Launch_with_a_saved_password_signs_in_online_automatically()
    {
        var harness = new Harness(rememberedEmail: "user@example.com");
        harness.Credentials.Items[harness.Key("user@example.com")] = "saved";
        harness.Script(Replies.Plan());
        await harness.Session.LaunchAsync();

        Assert.Equal(AppPhase.SignedIn, harness.Session.Phase);
        var login = harness.Transport.Commands(IpcCommands.Login).Single().Payload!.Value;
        Assert.Equal("saved", login.GetProperty("password").GetString());
    }

    [Fact]
    public async Task Launch_with_an_unreadable_vault_asks_for_the_password_instead()
    {
        var harness = new Harness(rememberedEmail: "user@example.com");
        harness.Credentials.FailRead = true;
        harness.Script(Replies.Plan());
        await harness.Session.LaunchAsync();

        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Contains("凭据管理器", harness.Session.Warning);
        Assert.Empty(harness.Transport.Commands(IpcCommands.Login));
    }

    [Fact]
    public async Task A_remembered_password_is_not_a_remembered_session()
    {
        // No token is ever stored: every launch authenticates again, so an expired plan is seen at once.
        var harness = new Harness(rememberedEmail: "user@example.com");
        harness.Credentials.Items[harness.Key("user@example.com")] = "saved";
        harness.Script(Replies.Plan(authorized: false, reason: "subscription_expired"));
        await harness.Session.LaunchAsync();
        Assert.Single(harness.Transport.Commands(IpcCommands.Login));
        Assert.False(harness.Session.Snapshot.Entitlement!.AccountAuthorized);
        Assert.False(harness.Session.Connection.CanConnect);
    }

    [Fact]
    public async Task Unreachable_service_at_launch_is_a_service_problem_and_recovers_when_it_appears()
    {
        var harness = new Harness();
        harness.Transport.StartFailure = new CoreUnavailableException(ServiceErrors.ServiceNotInstalled, "not installed");
        await harness.Session.LaunchAsync();
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Contains("安装", harness.Session.ServiceProblem);

        harness.Transport.StartFailure = null;
        harness.Script(Replies.Plan());
        await harness.Session.ServiceBecameAvailableAsync();
        Assert.Null(harness.Session.ServiceProblem);
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Equal(2, harness.Transport.Starts);
    }

    [Fact]
    public async Task Service_refusing_hello_is_shown_as_a_service_problem_not_a_login_error()
    {
        var harness = new Harness();
        harness.Transport.StartFailure = new IpcCommandException(ServiceErrors.VersionMismatch, "mismatch");
        await harness.Session.SignInAsync("user@example.com", "pw", false);
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Contains("版本", harness.Session.Message);
        Assert.NotNull(harness.Session.ServiceProblem);
    }

    [Fact]
    public async Task Admin_without_a_plan_never_sends_connect()
    {
        var harness = await SignedIn(Replies.Plan(authorized: false, selected: Replies.NodeA, configReady: true, role: "admin", reason: "paid_vip_required"));
        await harness.Session.ConnectAsync();
        Assert.Empty(harness.Transport.Commands(IpcCommands.Connect));
    }

    [Fact]
    public async Task Connect_failure_inside_an_ok_reply_is_shown_and_is_not_connected()
    {
        var snapshot = Replies.Plan(selected: Replies.NodeA, configReady: true);
        var harness = new Harness();
        harness.Transport.Handler = (command, _, _) => Task.FromResult(command == IpcCommands.Connect
            ? Replies.Snapshot(Replies.Plan(state: "error", selected: Replies.NodeA, configReady: true, lastError: new ErrorView { Code = "tunnel_failed", Message = "隧道启动失败" }),
                new ErrorView { Code = "tunnel_failed", Message = "隧道启动失败" })
            : Replies.Snapshot(snapshot));
        await harness.Session.LaunchAsync();
        await harness.Session.SignInAsync("user@example.com", "pw", false);
        await harness.Session.ConnectAsync();

        Assert.Equal("隧道启动失败", harness.Session.Message);
        Assert.NotEqual("connected", harness.Session.Snapshot.State);
        Assert.False(harness.Session.Connection.ShowDisconnect);
    }

    [Fact]
    public async Task A_device_limit_at_connect_is_shown_and_the_account_stays_signed_in()
    {
        var limit = new ErrorView { Code = "device_limit", Message = "已达到同时在线设备上限，请先断开其他设备" };
        var snapshot = Replies.Plan(selected: Replies.NodeA, configReady: true);
        var harness = new Harness();
        harness.Transport.Handler = (command, _, _) => Task.FromResult(command == IpcCommands.Connect
            ? Replies.Snapshot(Replies.Plan(selected: Replies.NodeA, configReady: true, lastError: limit), limit)
            : Replies.Snapshot(snapshot));
        await harness.Session.LaunchAsync();
        await harness.Session.SignInAsync("user@example.com", "pw", false);
        await harness.Session.ConnectAsync();

        Assert.Equal(limit.Message, harness.Session.Message);
        Assert.Equal(AppPhase.SignedIn, harness.Session.Phase);
        Assert.True(harness.Session.Connection.CanConnect);
    }

    [Fact]
    public async Task Double_clicking_connect_sends_one_command()
    {
        var release = new TaskCompletionSource();
        var snapshot = Replies.Plan(selected: Replies.NodeA, configReady: true);
        var harness = new Harness();
        harness.Transport.Handler = async (command, _, _) =>
        {
            if (command == IpcCommands.Connect) await release.Task;
            return Replies.Snapshot(command == IpcCommands.Connect ? Replies.Plan(state: "connected", selected: Replies.NodeA, configReady: true) : snapshot);
        };
        await harness.Session.LaunchAsync();
        await harness.Session.SignInAsync("user@example.com", "pw", false);

        var first = harness.Session.ConnectAsync();
        Assert.Equal(SessionActivity.Connecting, harness.Session.Activity);
        await harness.Session.ConnectAsync(); // second click while the first is in flight
        release.SetResult();
        await first;

        Assert.Single(harness.Transport.Commands(IpcCommands.Connect));
        Assert.Equal("connected", harness.Session.Snapshot.State);
        Assert.Equal(SessionActivity.None, harness.Session.Activity);
    }

    [Fact]
    public async Task Selecting_a_line_quotes_generation_and_catalog_revision()
    {
        var harness = await SignedIn(Replies.Plan(generation: 9));
        await harness.Session.SelectAsync(Replies.NodeB);
        var sent = harness.Transport.Commands(IpcCommands.SelectNode).Single();
        Assert.Equal(9UL, sent.Generation);
        Assert.Equal(new string('a', 64), sent.Payload!.Value.GetProperty("catalog_revision").GetString());
    }

    [Fact]
    public async Task Stale_generation_refreshes_the_state_and_tells_the_user()
    {
        var snapshot = Replies.Plan(selected: Replies.NodeA, configReady: true);
        var harness = new Harness();
        harness.Transport.Handler = (command, _, _) => Task.FromResult(command == IpcCommands.Connect
            ? Replies.Refused("stale_generation", "状态已变化，请刷新后重试")
            : Replies.Snapshot(command == IpcCommands.ReadSnapshot ? Replies.Plan(generation: 8, selected: Replies.NodeA, configReady: true) : snapshot));
        await harness.Session.LaunchAsync();
        await harness.Session.SignInAsync("user@example.com", "pw", false);
        await harness.Session.ConnectAsync();

        Assert.Equal(8, harness.Session.Snapshot.Generation);
        Assert.Contains("状态已变化", harness.Session.Message);
        Assert.Equal(AppPhase.SignedIn, harness.Session.Phase);
    }

    [Fact]
    public async Task A_late_snapshot_from_an_older_generation_is_dropped()
    {
        var harness = await SignedIn(Replies.Plan(generation: 9));
        harness.Script(Replies.Plan(generation: 9), (command, _, _) => Replies.Snapshot(Replies.Plan(generation: 4, state: "connected")));
        await harness.Session.PollAsync();
        Assert.Equal(9, harness.Session.Snapshot.Generation);
        Assert.Equal("ready", harness.Session.Snapshot.State);
    }

    [Fact]
    public async Task Catalog_changes_arrive_through_the_snapshot_poll()
    {
        var harness = await SignedIn(Replies.Plan(generation: 5));
        var renamed = Replies.Plan(generation: 5, nodes: [new NodeView { Id = Replies.NodeA, Name = "新名称", Region = "日本", LineType = "dedicated", RatePermille = 750 }]);
        harness.Script(Replies.Plan(generation: 5), (_, _, _) => Replies.Snapshot(renamed));
        await harness.Session.PollAsync();
        var rows = NodeList.Build(harness.Session.Snapshot, "", LineFilter.All);
        Assert.Equal("新名称", Assert.Single(rows).Name);
        Assert.Equal("0.75×", rows[0].RateLabel);
    }

    [Fact]
    public async Task Core_ending_the_session_returns_to_login_and_relogs_in_once_with_a_saved_password()
    {
        var harness = new Harness(rememberedEmail: "user@example.com");
        harness.Credentials.Items[harness.Key("user@example.com")] = "saved";
        var expired = Replies.SignedOut(new ErrorView { Code = "native_session_expired", Message = "登录已失效，请重新登录" });
        var logins = 0;
        harness.Transport.Handler = (command, _, _) => Task.FromResult(command == IpcCommands.Login
            ? Replies.Snapshot(++logins <= 2 ? Replies.Plan() : Replies.Plan(generation: 99))
            : Replies.Snapshot(expired));
        await harness.Session.LaunchAsync();
        Assert.Equal(1, logins);

        await harness.Session.PollAsync(); // the Core reports the session as expired
        await Task.Delay(50);
        Assert.Equal(2, logins); // one silent re-login

        await harness.Session.PollAsync(); // expires again
        await Task.Delay(50);
        Assert.Equal(2, logins); // not a second one: no hammering the login endpoint
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Contains("失效", harness.Session.Message);
    }

    [Fact]
    public async Task Core_ending_the_session_without_a_saved_password_just_asks_to_sign_in()
    {
        var harness = await SignedIn();
        var expired = Replies.SignedOut(new ErrorView { Code = "subscription_changed", Message = "套餐已变更，请重新登录" });
        harness.Script(Replies.Plan(), (_, _, _) => Replies.Snapshot(expired));
        await harness.Session.PollAsync();
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Equal("套餐已变更，请重新登录", harness.Session.Message);
        Assert.Single(harness.Transport.Commands(IpcCommands.Login));
    }

    [Fact]
    public async Task Losing_the_worker_signs_out_and_resumes_nothing()
    {
        var harness = await SignedIn(Replies.Plan(state: "connected", selected: Replies.NodeA, configReady: true));
        harness.Script(Replies.Plan(), (_, _, _) => throw new CoreUnavailableException(ServiceErrors.WorkerExited, "pipe closed"));
        await harness.Session.PollAsync();
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Equal("signed_out", harness.Session.Snapshot.State);
        Assert.Contains("重新登录", harness.Session.Message);
    }

    [Fact]
    public async Task Sign_out_clears_the_saved_password_and_the_remembered_email()
    {
        var harness = await SignedIn(remember: true);
        harness.Script(Replies.SignedOut());
        await harness.Session.SignOutAsync();
        Assert.Empty(harness.Credentials.Items);
        Assert.Null(harness.Preferences.RememberedEmail);
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Single(harness.Transport.Commands(IpcCommands.Logout));
    }

    [Fact]
    public async Task Sign_out_still_clears_credentials_when_the_core_is_gone()
    {
        var harness = await SignedIn(remember: true);
        harness.Script(Replies.Plan(), (_, _, _) => throw new CoreUnavailableException(ServiceErrors.WorkerExited, "gone"));
        await harness.Session.SignOutAsync();
        Assert.Empty(harness.Credentials.Items);
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
    }

    [Fact]
    public async Task Sign_out_reports_a_vault_that_cannot_delete()
    {
        var harness = await SignedIn(remember: true);
        harness.Credentials.FailDelete = true;
        harness.Script(Replies.SignedOut());
        await harness.Session.SignOutAsync();
        Assert.Contains("手动删除", harness.Session.Warning);
    }

    [Fact]
    public async Task Shutdown_keeps_the_saved_password_and_stops_the_transport()
    {
        var harness = await SignedIn(remember: true);
        await harness.Session.ShutdownAsync();
        Assert.Single(harness.Credentials.Items);
        Assert.Equal(1, harness.Transport.Stops);
    }

    [Fact]
    public async Task Shutdown_while_connected_disconnects_first()
    {
        var harness = await SignedIn(Replies.Plan(state: "connected", selected: Replies.NodeA, configReady: true));
        harness.Script(Replies.Plan(state: "connected"), (_, _, _) => Replies.Snapshot(Replies.Plan(state: "ready")));
        await harness.Session.ShutdownAsync();
        Assert.Single(harness.Transport.Commands(IpcCommands.Disconnect));
        Assert.Equal(1, harness.Transport.Stops);
    }

    [Fact]
    public async Task Cancelling_a_connect_ends_the_session_by_closing_the_pipe()
    {
        var release = new TaskCompletionSource();
        var harness = new Harness();
        var snapshot = Replies.Plan(selected: Replies.NodeA, configReady: true);
        harness.Transport.Handler = async (command, _, _) =>
        {
            if (command == IpcCommands.Connect)
            {
                await release.Task;
                throw new CoreUnavailableException(ServiceErrors.WorkerExited, "pipe closed");
            }
            return Replies.Snapshot(snapshot);
        };
        await harness.Session.LaunchAsync();
        await harness.Session.SignInAsync("user@example.com", "pw", false);

        var connect = harness.Session.ConnectAsync();
        await harness.Session.CancelOperationAsync();
        release.SetResult(); // the closed pipe makes the pending read fail
        await connect;

        Assert.Equal(1, harness.Transport.Stops);
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Contains("已取消", harness.Session.Message);
    }

    [Fact]
    public async Task The_password_is_never_kept_by_the_controller_or_its_messages()
    {
        var harness = await SignedIn(remember: false);
        var state = string.Join("|", harness.Session.Message, harness.Session.Warning, harness.Session.Email, harness.Session.ServiceProblem,
            JsonSerializer.Serialize(harness.Session.Snapshot, SnapshotJson.Options));
        Assert.DoesNotContain("pw", state.Replace("password", ""), StringComparison.Ordinal);
    }

    [Fact]
    public void Website_links_come_from_the_build_configuration()
    {
        var harness = new Harness();
        Assert.Equal("https://site.example.test/plans", harness.Session.PlansUrl.AbsoluteUri);
        Assert.Equal("https://site.example.test/register", harness.Session.RegisterUrl.AbsoluteUri);
        Assert.Equal("https://site.example.test/forgot-password", harness.Session.ForgotPasswordUrl.AbsoluteUri);
    }
}

/// <summary>handoff_auth.md R4-03: the password was reset on the website while the old one was saved here.</summary>
public class PasswordResetTests
{
    private static readonly ErrorView Refused = new() { Code = "invalid_credentials", Message = "邮箱或密码不正确" };

    /// <summary>Logins succeed only with <paramref name="accepted"/>; <paramref name="onLogin"/> runs while a login is on the wire.</summary>
    private static List<string> Server(Harness harness, string accepted, Action<string>? onLogin = null)
    {
        var passwords = new List<string>();
        harness.Transport.Handler = (command, _, payload) =>
        {
            if (command != IpcCommands.Login) return Task.FromResult(Replies.Snapshot(Replies.Plan()));
            var password = payload!.Value.GetProperty("password").GetString()!;
            passwords.Add(password);
            onLogin?.Invoke(password);
            return Task.FromResult(password == accepted ? Replies.Snapshot(Replies.Plan()) : Replies.Snapshot(Replies.SignedOut(), Refused));
        };
        return passwords;
    }

    [Fact]
    public async Task The_old_saved_password_is_tried_once_then_forgotten_and_the_new_one_saved()
    {
        var harness = new Harness(rememberedEmail: "user@example.com");
        harness.Credentials.Items[harness.Key("user@example.com")] = "old-password";
        var passwords = Server(harness, accepted: "new-password");
        await harness.Session.LaunchAsync();

        Assert.Equal(["old-password"], passwords);
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Equal("邮箱或密码不正确", harness.Session.Message);
        Assert.Empty(harness.Credentials.Items);
        Assert.Equal("user@example.com", harness.Preferences.RememberedEmail); // the non-secret email still prefills the form
        Assert.Equal("user@example.com", harness.Session.Email);

        await harness.Session.SignInAsync("user@example.com", "new-password", remember: true);
        Assert.Equal(AppPhase.SignedIn, harness.Session.Phase);
        Assert.Equal("new-password", harness.Credentials.Items[harness.Key("user@example.com")]);
        Assert.Equal(["old-password", "new-password"], passwords);
    }

    [Fact]
    public async Task A_typo_in_a_manual_login_keeps_the_saved_password()
    {
        var harness = new Harness();
        harness.Credentials.Items[harness.Key("user@example.com")] = "good-password";
        Server(harness, accepted: "good-password");
        await harness.Session.LaunchAsync();
        await harness.Session.SignInAsync("user@example.com", "typo-password", remember: true);
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Equal("good-password", harness.Credentials.Items[harness.Key("user@example.com")]);
    }

    [Fact]
    public async Task A_refusal_never_deletes_a_password_saved_meanwhile()
    {
        var harness = new Harness();
        var key = harness.Key("user@example.com");
        harness.Credentials.Items[key] = "old-password";
        // While the old password is on its way to the server, another login saves the new one.
        Server(harness, accepted: "new-password", onLogin: p => { if (p == "old-password") harness.Credentials.Items[key] = "new-password"; });
        await harness.Session.LaunchAsync();
        await harness.Session.SignInAsync("user@example.com", "old-password", remember: true);
        Assert.Equal(AppPhase.SignedOut, harness.Session.Phase);
        Assert.Equal("new-password", harness.Credentials.Items[key]);
    }
}

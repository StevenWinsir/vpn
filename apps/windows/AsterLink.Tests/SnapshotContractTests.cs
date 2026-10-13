using System.Text.Json;
using AsterLink.Shared.Snapshot;

namespace AsterLink.Tests;

public class SnapshotContractTests
{
    private static JsonElement Fixture(string name) => ContractFiles.SharedContract("snapshot-v1.json").GetProperty("snapshots").GetProperty(name);

    public static TheoryData<string> FixtureNames => new(
        ContractFiles.SharedContract("snapshot-v1.json").GetProperty("snapshots").EnumerateObject().Select(p => p.Name));

    [Fact]
    public void Contract_has_the_fixtures_this_suite_depends_on()
    {
        var names = ContractFiles.SharedContract("snapshot-v1.json").GetProperty("snapshots").EnumerateObject().Select(p => p.Name).ToHashSet();
        Assert.Superset(new HashSet<string> { "signed_out_after_expiry", "ready_with_selection", "connected_metering", "connected_node_reported", "blocked_no_plan" }, names);
    }

    [Theory]
    [MemberData(nameof(FixtureNames))]
    public void Every_shared_fixture_decodes(string name)
    {
        var snapshot = SnapshotJson.ParseSnapshot(Fixture(name));
        Assert.NotEmpty(snapshot.State);
        Assert.True(snapshot.Generation > 0);
        Assert.NotNull(snapshot.Nodes);
    }

    [Fact]
    public void Signed_out_snapshot_has_no_user_entitlement_or_metering_but_keeps_the_error()
    {
        var snapshot = SnapshotJson.ParseSnapshot(Fixture("signed_out_after_expiry"));
        Assert.Equal("signed_out", snapshot.State);
        Assert.Null(snapshot.User);
        Assert.Null(snapshot.Entitlement);
        Assert.Null(snapshot.Metering);
        Assert.Equal("native_session_expired", snapshot.LastError?.Code);
        Assert.Equal("登录已失效，请重新登录", snapshot.LastError?.Message);
    }

    [Fact]
    public void Dates_are_parsed_as_utc_instants()
    {
        var entitlement = SnapshotJson.ParseSnapshot(Fixture("ready_with_selection")).Entitlement!;
        Assert.Equal(new DateTimeOffset(2030, 1, 1, 0, 10, 0, TimeSpan.Zero), entitlement.SubscriptionExpiresAt);
        Assert.Equal(new DateTimeOffset(2030, 1, 1, 0, 1, 30, TimeSpan.Zero), entitlement.AuthorizationExpiresAt);
        Assert.Equal(87, entitlement.LeaseRemainingSeconds);
    }

    [Fact]
    public void Null_dates_stay_null_for_an_account_without_a_plan()
    {
        var snapshot = SnapshotJson.ParseSnapshot(Fixture("blocked_no_plan"));
        Assert.Equal("blocked", snapshot.State);
        Assert.Equal("admin", snapshot.User?.Role);
        Assert.Null(snapshot.Entitlement!.SubscriptionExpiresAt);
        Assert.Null(snapshot.Entitlement.AuthorizationExpiresAt);
        // Admin role alone must never grant proxy access.
        Assert.False(snapshot.Entitlement.AccountAuthorized);
        Assert.False(snapshot.Entitlement.CanConnect);
        Assert.Equal("upgrade_required", snapshot.Entitlement.Reason);
    }

    [Fact]
    public void Node_metadata_carries_rate_and_metering_source_but_nothing_else()
    {
        var standard = SnapshotJson.ParseSnapshot(Fixture("ready_with_selection")).Nodes;
        Assert.Equal([1000L, 500L], standard.Select(n => n.RatePermille));
        Assert.Equal(["dedicated", "direct"], standard.Select(n => n.LineType));
        Assert.All(standard, n => Assert.Equal("", n.MeteringSource));

        var authoritative = SnapshotJson.ParseSnapshot(Fixture("connected_node_reported")).Nodes.Single();
        Assert.Equal("node_reported", authoritative.MeteringSource);
        Assert.Equal(500, authoritative.RatePermille);
    }

    [Fact]
    public void Metering_matches_the_documented_example()
    {
        // 20 MiB up + 80 MiB down at 1000 permille = 104,857,600,000 units (handoff section 8.1).
        var snapshot = SnapshotJson.ParseSnapshot(Fixture("connected_metering"));
        Assert.Equal(104_857_600_000L, snapshot.Entitlement!.UsedUnits);
        var metering = snapshot.Metering!;
        Assert.Equal("client_reported", metering.Source);
        Assert.Equal(3, metering.LastSequence);
        Assert.Equal(1_048_576, metering.DownloadBytesPerSecond);
        Assert.False(metering.Pending);
        Assert.Equal(3, metering.History.Count);
        Assert.Equal(524_288, metering.History[1].Download);
    }

    [Fact]
    public void Integers_above_2_pow_53_are_exact()
    {
        const long above = 9_007_199_254_740_993; // 2^53 + 1, not representable as a double
        var snapshot = SnapshotJson.ParseSnapshot(
            """{"generation":1,"state":"ready","entitlement":{"total_bytes":""" + above + ""","used_units":""" + long.MaxValue + "}}");
        Assert.Equal(above, snapshot.Entitlement!.TotalBytes);
        Assert.Equal(long.MaxValue, snapshot.Entitlement.UsedUnits);
        Assert.NotEqual(above, (long)(double)above);
    }

    [Theory]
    [InlineData("""{"generation":1,"entitlement":{"used_units":9223372036854775808}}""")] // long.MaxValue + 1
    [InlineData("""{"generation":1,"entitlement":{"used_units":1.5}}""")]
    [InlineData("""{"generation":1,"entitlement":{"used_units":"12"}}""")]
    [InlineData("""{"generation":"1"}""")]
    public void Lossy_or_coerced_numbers_are_refused(string json)
    {
        Assert.Throws<JsonException>(() => SnapshotJson.ParseSnapshot(json));
    }

    [Fact]
    public void Garbage_and_trailing_content_are_refused()
    {
        Assert.Throws<JsonException>(() => SnapshotJson.ParseSnapshot("not json"));
        Assert.Throws<JsonException>(() => SnapshotJson.ParseSnapshot("""{"generation":1}{"generation":2}"""));
        Assert.Throws<JsonException>(() => SnapshotJson.ParseSnapshot("null"));
    }

    [Fact]
    public void Go_style_nanosecond_timestamps_are_accepted()
    {
        var snapshot = SnapshotJson.ParseSnapshot(
            """{"generation":1,"entitlement":{"subscription_expires_at":"2030-01-01T00:10:00.123456789Z"}}""");
        var expires = snapshot.Entitlement!.SubscriptionExpiresAt!.Value;
        Assert.Equal(new DateTime(2030, 1, 1, 0, 10, 0, DateTimeKind.Utc).AddTicks(1_234_567), expires.UtcDateTime);
    }

    [Fact]
    public void No_snapshot_model_can_hold_a_secret()
    {
        // Structural guard: if someone adds a field that could carry connection material or a
        // credential, this fails and forces a review of the redaction contract.
        var banned = new[] { "password", "token", "secret", "yaml", "config", "server", "address", "port", "uuid", "host", "key", "credential" };
        var types = typeof(CoreSnapshot).Assembly.GetTypes()
            .Where(t => t.Namespace == typeof(CoreSnapshot).Namespace && t.IsClass && t.IsPublic && t != typeof(SnapshotJson));
        foreach (var type in types)
            foreach (var property in type.GetProperties())
            {
                var name = property.Name.ToLowerInvariant();
                // ConfigReady is a boolean readiness flag, not configuration text.
                if (name == "configready") continue;
                Assert.DoesNotContain(banned, word => name.Contains(word));
            }
    }
}

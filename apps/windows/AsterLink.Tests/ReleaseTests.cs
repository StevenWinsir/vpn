using AsterLink.Shared.Client;
using AsterLink.Shared.Service;

namespace AsterLink.Tests;

/// <summary>Round 8: upgrade classification and the per-user uninstall cleanup (portable parts).</summary>
public class ReleaseTests
{
    [Theory]
    [InlineData(null, "0.1.0", UpgradeKind.Fresh)]
    [InlineData("garbage", "0.1.0", UpgradeKind.Fresh)]
    [InlineData("0.1.0", "0.1.0", UpgradeKind.Reinstall)]
    [InlineData("0.1.0", "0.1.1", UpgradeKind.Upgrade)]
    [InlineData("0.9.9", "0.10.0", UpgradeKind.Upgrade)]
    [InlineData("1.0.0", "0.99.99", UpgradeKind.Downgrade)]
    [InlineData("0.2.0", "0.1.9", UpgradeKind.Downgrade)]
    public void Versions_are_classified_numerically_not_as_text(string? installed, string incoming, UpgradeKind expected) =>
        Assert.Equal(expected, UpgradePolicy.Classify(installed, incoming));

    [Fact]
    public void A_downgrade_needs_an_explicit_request_and_everything_else_is_allowed()
    {
        Assert.False(UpgradePolicy.Allowed(UpgradeKind.Downgrade, allowDowngrade: false));
        Assert.True(UpgradePolicy.Allowed(UpgradeKind.Downgrade, allowDowngrade: true));
        foreach (var kind in new[] { UpgradeKind.Fresh, UpgradeKind.Reinstall, UpgradeKind.Upgrade })
            Assert.True(UpgradePolicy.Allowed(kind, allowDowngrade: false));
    }

    [Fact]
    public void A_malformed_incoming_version_is_rejected() =>
        Assert.Throws<ArgumentException>(() => UpgradePolicy.Classify("0.1.0", "0.1"));

    [Fact]
    public void Cleanup_does_only_what_was_chosen()
    {
        var directory = Path.Combine(Path.GetTempPath(), "asterlink-cleanup-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(directory);
        File.WriteAllText(Path.Combine(directory, "settings.json"), "{}");
        try
        {
            var calls = 0;
            var nothing = UserDataCleanup.Run(passwords: false, preferences: false, () => { calls++; return 5; }, directory);
            Assert.Equal(0, calls);
            Assert.Equal(new UserCleanupResult(0, false, null), nothing);
            Assert.True(Directory.Exists(directory));

            var passwordsOnly = UserDataCleanup.Run(passwords: true, preferences: false, () => { calls++; return 2; }, directory);
            Assert.Equal(new UserCleanupResult(2, false, null), passwordsOnly);
            Assert.True(Directory.Exists(directory));

            var both = UserDataCleanup.Run(passwords: true, preferences: true, () => 0, directory);
            Assert.Null(both.Error);
            Assert.True(both.PreferencesRemoved);
            Assert.False(Directory.Exists(directory));
        }
        finally { if (Directory.Exists(directory)) Directory.Delete(directory, recursive: true); }
    }

    [Fact]
    public void A_vault_failure_is_reported_and_does_not_stop_the_preference_removal()
    {
        var directory = Path.Combine(Path.GetTempPath(), "asterlink-cleanup-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(directory);
        try
        {
            var result = UserDataCleanup.Run(true, true,
                () => throw new CredentialStoreException("vault unavailable"), directory);
            Assert.Contains("vault unavailable", result.Error);
            Assert.True(result.PreferencesRemoved);
        }
        finally { if (Directory.Exists(directory)) Directory.Delete(directory, recursive: true); }
    }

    [Fact]
    public void A_missing_preference_folder_is_not_an_error()
    {
        var missing = Path.Combine(Path.GetTempPath(), "asterlink-cleanup-" + Guid.NewGuid().ToString("N"));
        Assert.Equal(new UserCleanupResult(0, false, null), UserDataCleanup.Run(false, true, () => 0, missing));
    }
}

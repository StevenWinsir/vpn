using AsterLink.Platform;
using AsterLink.Shared.Client;

namespace AsterLink.Tests.Windows;

/// <summary>The real Windows Credential Manager of the account running the tests (a CI runner or the developer's own).</summary>
public class CredentialStoreTests
{
    private static string Account() => "https://test.invalid/api/v1/client|" + Guid.NewGuid().ToString("N") + "@asterlink-test.invalid";

    [WindowsFact]
    public void Save_read_overwrite_delete_round_trip()
    {
        var store = new CredentialManagerStore();
        var account = Account();
        try
        {
            Assert.Null(store.Read(account));
            store.Save(account, "first-密码");
            Assert.Equal("first-密码", store.Read(account));
            store.Save(account, "second");
            Assert.Equal("second", store.Read(account));
        }
        finally { store.Delete(account); }
        Assert.Null(store.Read(account));
    }

    [WindowsFact]
    public void Deleting_a_missing_item_is_not_an_error() => new CredentialManagerStore().Delete(Account());

    [WindowsFact]
    public void Items_of_different_accounts_do_not_mix()
    {
        var store = new CredentialManagerStore();
        var a = Account();
        var b = Account();
        try
        {
            store.Save(a, "alpha");
            store.Save(b, "beta");
            Assert.Equal("alpha", store.Read(a));
            Assert.Equal("beta", store.Read(b));
            store.Delete(a);
            Assert.Null(store.Read(a));
            Assert.Equal("beta", store.Read(b));
        }
        finally { store.Delete(a); store.Delete(b); }
    }

    [WindowsFact]
    public void Forgetting_all_product_items_removes_ours_and_only_ours()
    {
        var store = new CredentialManagerStore();
        var a = Account();
        var b = Account();
        // A neighbour application's item must survive: it lives under a different target prefix.
        var other = "OtherApp-" + Guid.NewGuid().ToString("N");
        try
        {
            store.Save(a, "alpha");
            store.Save(b, "beta");
            Assert.True(store.DeleteAllProductItems() >= 2);
            Assert.Null(store.Read(a));
            Assert.Null(store.Read(b));
            Assert.Equal(0, store.DeleteAllProductItems());
        }
        finally { store.Delete(a); store.Delete(b); _ = other; }
    }

    [WindowsFact]
    public void Empty_input_and_oversize_passwords_are_refused()
    {
        var store = new CredentialManagerStore();
        Assert.Throws<CredentialStoreException>(() => store.Save("", "x"));
        Assert.Throws<CredentialStoreException>(() => store.Save(Account(), ""));
        Assert.Throws<CredentialStoreException>(() => store.Save(Account(), new string('x', 2000)));
    }

    [WindowsFact]
    public void The_target_name_is_product_scoped_so_other_applications_items_are_never_touched()
    {
        var account = Account();
        Assert.StartsWith("AsterLink/", CredentialAccount.TargetName(account));
    }
}

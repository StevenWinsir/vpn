using System.Diagnostics;
using AsterLink.Platform;
using AsterLink.Shared.Service;

namespace AsterLink.Tests.Windows;

public class FileProtectionTests
{
    [AdminFact]
    public void A_user_writable_temp_tree_is_not_protected_until_protect_is_applied()
    {
        using var scratch = new Scratch();
        var layout = Stage.Build(scratch.Root);
        Assert.NotEmpty(ProtectedFiles.Check(layout)); // %TEMP% is writable by the user that made it

        // Re-create the tree inside a protected root, the way the installer does.
        using var target = new Scratch();
        Directory.Delete(target.Root);
        Directory.CreateDirectory(target.Root);
        ProtectedFiles.Protect(target.Root, usersMayRead: true);
        foreach (var name in InstallLayout.ManagedDirectories) CopyDir(layout.Join(name), System.IO.Path.Combine(target.Root, name));
        File.Copy(layout.BuildConfig, System.IO.Path.Combine(target.Root, ServiceConstants.BuildConfigFile));
        File.Copy(layout.Manifest, System.IO.Path.Combine(target.Root, ServiceConstants.ManifestFile));

        var protectedLayout = new InstallLayout(target.Root);
        Assert.Empty(ProtectedFiles.Check(protectedLayout));
        var report = InstallVerifier.Verify(protectedLayout);
        Assert.True(report.Trusted, string.Join("; ", report.Problems));
    }

    [AdminFact]
    public void A_group_that_can_write_is_found_even_below_a_protected_root()
    {
        using var scratch = new Scratch();
        Directory.Delete(scratch.Root);
        Directory.CreateDirectory(scratch.Root);
        ProtectedFiles.Protect(scratch.Root, usersMayRead: true);
        var layout = Stage.Build(scratch.Root);

        // Grant the Users group write on the core folder: exactly the mistake that lets a user swap the Core.
        var core = new DirectoryInfo(layout.Join("core"));
        var security = core.GetAccessControl();
        security.AddAccessRule(new System.Security.AccessControl.FileSystemAccessRule(
            new System.Security.Principal.SecurityIdentifier(System.Security.Principal.WellKnownSidType.BuiltinUsersSid, null),
            System.Security.AccessControl.FileSystemRights.Modify, System.Security.AccessControl.InheritanceFlags.ContainerInherit | System.Security.AccessControl.InheritanceFlags.ObjectInherit,
            System.Security.AccessControl.PropagationFlags.None, System.Security.AccessControl.AccessControlType.Allow));
        core.SetAccessControl(security);

        Assert.Contains(ProtectedFiles.Check(layout), p => p.Contains("may modify"));

        ProtectedFiles.ResetInheritedAcls(scratch.Root);
        Assert.Empty(ProtectedFiles.Check(layout));
    }

    [AdminFact]
    public void A_junction_inside_the_install_tree_is_refused()
    {
        using var scratch = new Scratch();
        Directory.Delete(scratch.Root);
        Directory.CreateDirectory(scratch.Root);
        ProtectedFiles.Protect(scratch.Root, usersMayRead: true);
        var layout = Stage.Build(scratch.Root);

        using var elsewhere = new Scratch();
        var junction = layout.Join("core/redirect");
        var cmd = Process.Start(new ProcessStartInfo("cmd.exe", $"/c mklink /J \"{junction}\" \"{elsewhere.Root}\"") { UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true })!;
        cmd.WaitForExit();
        Assert.Equal(0, cmd.ExitCode);

        Assert.Contains(ProtectedFiles.Check(layout), p => p.Contains("reparse point"));
    }

    [WindowsFact]
    public void A_modified_core_fails_verification_even_in_a_protected_tree()
    {
        using var scratch = new Scratch();
        var layout = Stage.Build(scratch.Root);
        File.WriteAllText(layout.CoreExe, "tampered");
        var report = InstallVerifier.Verify(layout);
        Assert.False(report.Trusted);
        Assert.Contains(report.Problems, p => p.Contains("asterlink-core.exe"));
    }

    [WindowsFact]
    public void A_planted_dll_next_to_the_core_fails_verification()
    {
        using var scratch = new Scratch();
        var layout = Stage.Build(scratch.Root);
        File.WriteAllText(layout.Join("core/version.dll"), "planted");
        Assert.Contains(InstallVerifier.Verify(layout).Problems, p => p.Contains("UnlistedFile") && p.Contains("version.dll"));
    }

    [WindowsFact]
    public void A_release_channel_manifest_is_not_accepted_by_this_build()
    {
        using var scratch = new Scratch();
        var layout = Stage.Build(scratch.Root);
        var text = File.ReadAllText(layout.Manifest).Replace("\"internal\"", "\"release\"");
        File.WriteAllText(layout.Manifest, text);
        Assert.Contains(InstallVerifier.Verify(layout).Problems, p => p.Contains("internal channel"));
    }

    private static void CopyDir(string source, string destination)
    {
        Directory.CreateDirectory(destination);
        foreach (var entry in new DirectoryInfo(source).EnumerateFileSystemInfos())
        {
            var target = System.IO.Path.Combine(destination, entry.Name);
            if (entry is DirectoryInfo directory) CopyDir(directory.FullName, target); else File.Copy(entry.FullName, target);
        }
    }
}

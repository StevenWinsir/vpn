using System.Security.AccessControl;
using System.Security.Principal;
using AsterLink.Shared.Service;

namespace AsterLink.Platform;

/// <summary>
/// Checks that the install tree is what the service may execute as SYSTEM: no reparse point
/// anywhere in it, and no location a standard user can write. Content is checked separately
/// against the manifest; this is about where the content lives.
/// </summary>
public static class ProtectedFiles
{
    public static IReadOnlyList<string> Check(InstallLayout layout)
    {
        var problems = new List<string>();
        if (!WinPath.IsSafeAbsolute(layout.Root)) { problems.Add("install root is not a plain absolute path"); return problems; }
        if (!Directory.Exists(layout.Root)) { problems.Add("install root does not exist"); return problems; }

        CheckDirectory(layout.Root, problems);
        foreach (var name in InstallLayout.ManagedDirectories)
        {
            var directory = layout.Join(name);
            if (!Directory.Exists(directory)) { problems.Add($"{name} directory is missing"); continue; }
            CheckDirectory(directory, problems);
            foreach (var sub in Directory.EnumerateDirectories(directory, "*", SearchOption.AllDirectories))
                CheckDirectory(sub, problems);
        }
        foreach (var file in new[] { layout.Manifest, layout.BuildConfig, layout.AppExe, layout.ServiceExe, layout.CoreExe })
        {
            if (!File.Exists(file)) continue; // absence is the manifest check's finding
            CheckAcl(new FileInfo(file).GetAccessControl(), file, problems);
        }
        return problems;
    }

    private static void CheckDirectory(string path, List<string> problems)
    {
        var info = new DirectoryInfo(path);
        if ((info.Attributes & FileAttributes.ReparsePoint) != 0 || info.LinkTarget is not null)
        {
            problems.Add($"{path} is a reparse point");
            return;
        }
        CheckAcl(info.GetAccessControl(), path, problems);
    }

    private static void CheckAcl(FileSystemSecurity security, string path, List<string> problems)
    {
        var owner = (security.GetOwner(typeof(SecurityIdentifier)) as SecurityIdentifier)?.Value;
        var entries = security.GetAccessRules(true, true, typeof(SecurityIdentifier))
            .Cast<FileSystemAccessRule>()
            .Select(rule => new AceFact(
                rule.IdentityReference.Value,
                rule.AccessControlType == AccessControlType.Allow,
                (int)rule.FileSystemRights,
                rule.PropagationFlags.HasFlag(PropagationFlags.InheritOnly)));
        var verdict = AclPolicy.Evaluate(owner, entries);
        if (!verdict.Safe) problems.Add($"{path}: {verdict.Detail}");
    }

    /// <summary>
    /// Gives a directory the protected ACL: SYSTEM and Administrators full control, ordinary users
    /// read-and-execute (or nothing), inheritance from the parent cut so a permissive parent cannot leak in.
    /// Apply it while the directory is empty; files created later inherit it.
    /// </summary>
    public static void Protect(string directory, bool usersMayRead)
    {
        var security = new DirectorySecurity();
        security.SetAccessRuleProtection(isProtected: true, preserveInheritance: false);
        var inherit = InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit;
        security.AddAccessRule(new FileSystemAccessRule(new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null), FileSystemRights.FullControl, inherit, PropagationFlags.None, AccessControlType.Allow));
        security.AddAccessRule(new FileSystemAccessRule(new SecurityIdentifier(WellKnownSidType.BuiltinAdministratorsSid, null), FileSystemRights.FullControl, inherit, PropagationFlags.None, AccessControlType.Allow));
        if (usersMayRead)
            security.AddAccessRule(new FileSystemAccessRule(new SecurityIdentifier(WellKnownSidType.BuiltinUsersSid, null), FileSystemRights.ReadAndExecute, inherit, PropagationFlags.None, AccessControlType.Allow));
        security.SetOwner(new SecurityIdentifier(WellKnownSidType.BuiltinAdministratorsSid, null));
        new DirectoryInfo(directory).SetAccessControl(security);
    }

    /// <summary>
    /// Puts every file and folder below <paramref name="root"/> back to inheriting the root's protected ACL,
    /// dropping any explicit entry someone added. Links are never followed.
    /// </summary>
    public static void ResetInheritedAcls(string root)
    {
        foreach (var entry in new DirectoryInfo(root).EnumerateFileSystemInfos("*", SearchOption.AllDirectories))
        {
            if (entry.LinkTarget is not null || (entry.Attributes & FileAttributes.ReparsePoint) != 0) continue;
            if (entry is DirectoryInfo directory)
            {
                var security = new DirectorySecurity();
                security.SetAccessRuleProtection(isProtected: false, preserveInheritance: false);
                directory.SetAccessControl(security);
            }
            else if (entry is FileInfo file)
            {
                var security = new FileSecurity();
                security.SetAccessRuleProtection(isProtected: false, preserveInheritance: false);
                file.SetAccessControl(security);
            }
        }
    }

    public static bool IsElevated()
    {
        using var identity = WindowsIdentity.GetCurrent();
        return new WindowsPrincipal(identity).IsInRole(WindowsBuiltInRole.Administrator);
    }
}

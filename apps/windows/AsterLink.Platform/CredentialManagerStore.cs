using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;
using System.Text;
using AsterLink.Shared.Client;

namespace AsterLink.Platform;

/// <summary>
/// "Remember password" on Windows: one Generic Credential per (API base, email) in the current user's
/// Credential Manager vault. Persistence is CRED_PERSIST_LOCAL_MACHINE, which stays on this computer and
/// does not roam with a domain profile; the vault itself is per Windows user, so another user of the same
/// PC cannot read it and the SYSTEM service never touches it. A vault failure is reported, never worked
/// around with a file or the registry.
/// </summary>
public sealed class CredentialManagerStore : ICredentialStore
{
    private const uint TypeGeneric = 1;
    private const uint PersistLocalMachine = 2;
    private const int ErrorNotFound = 1168;
    private const int MaxBlobBytes = 2560;

    [StructLayout(LayoutKind.Sequential)]
    private struct Credential
    {
        public uint Flags;
        public uint Type;
        public IntPtr TargetName;
        public IntPtr Comment;
        public FILETIME LastWritten;
        public uint CredentialBlobSize;
        public IntPtr CredentialBlob;
        public uint Persist;
        public uint AttributeCount;
        public IntPtr Attributes;
        public IntPtr TargetAlias;
        public IntPtr UserName;
    }

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true, EntryPoint = "CredWriteW")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CredWrite(ref Credential credential, uint flags);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true, EntryPoint = "CredReadW")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CredRead(string target, uint type, uint flags, out IntPtr credential);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true, EntryPoint = "CredDeleteW")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CredDelete(string target, uint type, uint flags);

    [DllImport("advapi32.dll")]
    private static extern void CredFree(IntPtr buffer);

    public void Save(string account, string password)
    {
        if (account.Length == 0 || password.Length == 0) throw new CredentialStoreException("empty account or password");
        var blob = Encoding.Unicode.GetBytes(password);
        if (blob.Length > MaxBlobBytes) throw new CredentialStoreException("password too long for the credential vault");

        var target = Marshal.StringToHGlobalUni(CredentialAccount.TargetName(account));
        var user = Marshal.StringToHGlobalUni(account.Contains('|') ? account[(account.IndexOf('|') + 1)..] : account);
        var data = Marshal.AllocHGlobal(blob.Length);
        try
        {
            Marshal.Copy(blob, 0, data, blob.Length);
            var credential = new Credential
            {
                Type = TypeGeneric,
                TargetName = target,
                UserName = user,
                CredentialBlobSize = (uint)blob.Length,
                CredentialBlob = data,
                Persist = PersistLocalMachine,
            };
            if (!CredWrite(ref credential, 0)) throw Failure("CredWrite");
        }
        finally
        {
            Array.Clear(blob);
            // Wipe the unmanaged copy before freeing it.
            for (var i = 0; i < blob.Length; i++) Marshal.WriteByte(data, i, 0);
            Marshal.FreeHGlobal(data);
            Marshal.FreeHGlobal(target);
            Marshal.FreeHGlobal(user);
        }
    }

    public string? Read(string account)
    {
        if (!CredRead(CredentialAccount.TargetName(account), TypeGeneric, 0, out var pointer))
        {
            var error = Marshal.GetLastWin32Error();
            if (error == ErrorNotFound) return null;
            throw new CredentialStoreException("CredRead failed: " + new Win32Exception(error).Message, error);
        }
        try
        {
            var credential = Marshal.PtrToStructure<Credential>(pointer);
            if (credential.CredentialBlobSize == 0 || credential.CredentialBlob == IntPtr.Zero) return null;
            var size = (int)Math.Min(credential.CredentialBlobSize, MaxBlobBytes);
            var bytes = new byte[size];
            Marshal.Copy(credential.CredentialBlob, bytes, 0, size);
            try { return Encoding.Unicode.GetString(bytes); }
            finally { Array.Clear(bytes); }
        }
        finally { CredFree(pointer); }
    }

    public void Delete(string account)
    {
        if (CredDelete(CredentialAccount.TargetName(account), TypeGeneric, 0)) return;
        var error = Marshal.GetLastWin32Error();
        if (error != ErrorNotFound) throw new CredentialStoreException("CredDelete failed: " + new Win32Exception(error).Message, error);
    }

    private static CredentialStoreException Failure(string api)
    {
        var error = Marshal.GetLastWin32Error();
        return new CredentialStoreException($"{api} failed: {new Win32Exception(error).Message}", error);
    }
}

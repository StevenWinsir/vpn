using System.ComponentModel;
using System.IO.Pipes;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;

namespace AsterLink.Platform;

/// <summary>
/// One running Core. The Core is created suspended, put in a kill-on-close Job Object, and only
/// then resumed, so it can never run outside the job. If the service dies for any reason the
/// OS closes the job handle and kills the Core: there is no orphan proxy (handoff section 4.2).
/// Only the two pipe ends and a NUL handle are inherited, through an explicit handle list.
/// </summary>
public sealed class WorkerProcess : IDisposable
{
    private readonly SafeJobHandle job;
    private readonly SafeProcessHandle process;
    private readonly AnonymousPipeServerStream commands;
    private readonly AnonymousPipeServerStream replies;
    private readonly TaskCompletionSource<uint> exited = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly RegisteredWaitHandle waitRegistration;
    private readonly ManualResetEvent waitEvent;
    private readonly object gate = new();
    private bool disposed;

    public int ProcessId { get; }
    public long StartFileTime { get; }
    /// <summary>Where the service writes requests (the Core's command pipe).</summary>
    public Stream ToWorker => commands;
    /// <summary>Where the service reads the Core's replies.</summary>
    public Stream FromWorker => replies;
    /// <summary>Completes with the Core's exit code, however it ended.</summary>
    public Task<uint> Exited => exited.Task;

    private WorkerProcess(SafeJobHandle job, SafeProcessHandle process, int processId, long startFileTime,
        AnonymousPipeServerStream commands, AnonymousPipeServerStream replies)
    {
        this.job = job;
        this.process = process;
        ProcessId = processId;
        StartFileTime = startFileTime;
        this.commands = commands;
        this.replies = replies;
        waitEvent = new ManualResetEvent(false) { SafeWaitHandle = new SafeWaitHandle(process.DangerousGetHandle(), ownsHandle: false) };
        waitRegistration = ThreadPool.RegisterWaitForSingleObject(waitEvent, (_, _) =>
        {
            // Runs on a pool thread, possibly after Dispose has closed the handle: take the lock and look.
            lock (gate)
            {
                if (disposed) return;
                _ = Native.GetExitCodeProcess(process, out var code);
                exited.TrySetResult(code);
            }
        }, null, Timeout.Infinite, executeOnlyOnce: true);
    }

    /// <summary>
    /// Starts the Core at <paramref name="corePath"/>. <paramref name="persistBeforeRun"/> is called
    /// while the process exists but has not executed a single instruction; the service uses it to
    /// write the recovery record before anything can change the network. If it throws, the
    /// process is killed and never runs.
    /// </summary>
    public static WorkerProcess Start(string corePath, string workingDirectory, string tempDirectory,
        Action<int, long> persistBeforeRun)
    {
        var commands = new AnonymousPipeServerStream(PipeDirection.Out, HandleInheritability.Inheritable, 64 * 1024);
        var replies = new AnonymousPipeServerStream(PipeDirection.In, HandleInheritability.Inheritable, 64 * 1024);
        SafeJobHandle? job = null;
        SafeFileHandle? nul = null;
        var attributeList = IntPtr.Zero;
        var handleArray = IntPtr.Zero;
        var environment = IntPtr.Zero;
        var processInfo = new Native.ProcessInformation();
        var created = false;
        SafeProcessHandle? processHandle = null;
        try
        {
            // The service's own ends must never be inherited by anything it starts.
            ClearInherit(commands.SafePipeHandle);
            ClearInherit(replies.SafePipeHandle);

            var security = new Native.SecurityAttributes { Length = Marshal.SizeOf<Native.SecurityAttributes>(), InheritHandle = true };
            nul = Native.CreateFileW("NUL", Native.GenericRead | Native.GenericWrite, Native.FileShareReadWrite, ref security, Native.OpenExisting, 0, IntPtr.Zero);
            if (nul.IsInvalid) throw new Win32Exception(Marshal.GetLastWin32Error(), "cannot open NUL");

            var commandClient = commands.ClientSafePipeHandle.DangerousGetHandle();
            var replyClient = replies.ClientSafePipeHandle.DangerousGetHandle();
            var nulHandle = nul.DangerousGetHandle();

            job = CreateKillOnCloseJob();

            // Explicit handle list: exactly these three handles are inherited, nothing else.
            var list = new[] { nulHandle, commandClient, replyClient };
            handleArray = Marshal.AllocHGlobal(IntPtr.Size * list.Length);
            for (var i = 0; i < list.Length; i++) Marshal.WriteIntPtr(handleArray, i * IntPtr.Size, list[i]);

            var size = IntPtr.Zero;
            _ = Native.InitializeProcThreadAttributeList(IntPtr.Zero, 1, 0, ref size);
            attributeList = Marshal.AllocHGlobal(size);
            if (!Native.InitializeProcThreadAttributeList(attributeList, 1, 0, ref size))
                throw new Win32Exception(Marshal.GetLastWin32Error(), "InitializeProcThreadAttributeList");
            if (!Native.UpdateProcThreadAttribute(attributeList, 0, Native.ProcThreadAttributeHandleList, handleArray,
                    new IntPtr(IntPtr.Size * list.Length), IntPtr.Zero, IntPtr.Zero))
                throw new Win32Exception(Marshal.GetLastWin32Error(), "UpdateProcThreadAttribute");

            var startup = new Native.StartupInfoEx
            {
                StartupInfo = new Native.StartupInfo
                {
                    Cb = Marshal.SizeOf<Native.StartupInfoEx>(),
                    Flags = (int)Native.StartfUseStdHandles,
                    StdInput = nulHandle,
                    StdOutput = nulHandle,
                    StdError = nulHandle,
                },
                AttributeList = attributeList,
            };

            environment = BuildEnvironment(tempDirectory, commandClient.ToInt64(), replyClient.ToInt64());
            // The command line is only the quoted path: the Core takes no argument and none is ever passed.
            var commandLine = ("\"" + corePath + "\"\0").ToCharArray();

            if (!Native.CreateProcessW(corePath, commandLine, IntPtr.Zero, IntPtr.Zero, true,
                    Native.CreateSuspended | Native.ExtendedStartupInfoPresent | Native.CreateUnicodeEnvironment | Native.CreateNoWindow,
                    environment, workingDirectory, ref startup, out processInfo))
                throw new Win32Exception(Marshal.GetLastWin32Error(), "CreateProcess");
            created = true;

            if (!Native.AssignProcessToJobObject(job, processInfo.Process))
                throw new Win32Exception(Marshal.GetLastWin32Error(), "AssignProcessToJobObject");

            processHandle = new SafeProcessHandle(processInfo.Process, ownsHandle: true);
            processInfo.Process = IntPtr.Zero;
            _ = Native.GetProcessTimes(processHandle, out var startTime, out _, out _, out _);

            // Persist the recovery record while the Core is still frozen; a failure here kills it unrun.
            persistBeforeRun(processInfo.ProcessId, startTime);

            // The Core owns its ends now; closing ours makes a dead Core show up as end-of-file.
            commands.DisposeLocalCopyOfClientHandle();
            replies.DisposeLocalCopyOfClientHandle();

            if (Native.ResumeThread(processInfo.Thread) == uint.MaxValue)
                throw new Win32Exception(Marshal.GetLastWin32Error(), "ResumeThread");
            Native.CloseHandle(processInfo.Thread);
            processInfo.Thread = IntPtr.Zero;

            var worker = new WorkerProcess(job, processHandle, processInfo.ProcessId, startTime, commands, replies);
            job = null; // owned by the worker now
            return worker;
        }
        catch
        {
            // A suspended Core that never ran must not be left behind, whichever step failed.
            if (processHandle is not null) { _ = Native.TerminateProcess(processHandle, 1); processHandle.Dispose(); }
            else if (created && processInfo.Process != IntPtr.Zero)
            {
                using var raw = new SafeProcessHandle(processInfo.Process, ownsHandle: true);
                _ = Native.TerminateProcess(raw, 1);
                processInfo.Process = IntPtr.Zero;
            }
            commands.Dispose();
            replies.Dispose();
            throw;
        }
        finally
        {
            if (processInfo.Thread != IntPtr.Zero) Native.CloseHandle(processInfo.Thread);
            if (attributeList != IntPtr.Zero) { Native.DeleteProcThreadAttributeList(attributeList); Marshal.FreeHGlobal(attributeList); }
            if (handleArray != IntPtr.Zero) Marshal.FreeHGlobal(handleArray);
            if (environment != IntPtr.Zero) Marshal.FreeHGlobal(environment);
            nul?.Dispose();
            job?.Dispose(); // only reached non-null on failure: closing it kills a half-started Core
        }
    }

    private static void ClearInherit(SafeHandle handle)
    {
        if (!Native.SetHandleInformation(handle, Native.HandleFlagInherit, 0))
            throw new Win32Exception(Marshal.GetLastWin32Error(), "SetHandleInformation");
    }

    private static SafeJobHandle CreateKillOnCloseJob()
    {
        var job = Native.CreateJobObjectW(IntPtr.Zero, null);
        if (job.IsInvalid) throw new Win32Exception(Marshal.GetLastWin32Error(), "CreateJobObject");
        var info = new Native.JobExtendedLimit { Basic = new Native.JobBasicLimit { LimitFlags = Native.JobObjectLimitKillOnJobClose } };
        var size = Marshal.SizeOf<Native.JobExtendedLimit>();
        var buffer = Marshal.AllocHGlobal(size);
        try
        {
            Marshal.StructureToPtr(info, buffer, false);
            if (!Native.SetInformationJobObject(job, Native.JobObjectExtendedLimitInformation, buffer, size))
                throw new Win32Exception(Marshal.GetLastWin32Error(), "SetInformationJobObject");
        }
        finally { Marshal.FreeHGlobal(buffer); }
        return job;
    }

    /// <summary>A minimal, explicit environment: the Core inherits nothing from the service.</summary>
    private static IntPtr BuildEnvironment(string tempDirectory, long commandHandle, long replyHandle)
    {
        var systemRoot = Environment.GetEnvironmentVariable("SystemRoot") ?? @"C:\Windows";
        var variables = new SortedDictionary<string, string>(StringComparer.OrdinalIgnoreCase)
        {
            ["SystemRoot"] = systemRoot,
            ["windir"] = systemRoot,
            ["SystemDrive"] = Environment.GetEnvironmentVariable("SystemDrive") ?? "C:",
            ["PATH"] = systemRoot + @"\System32;" + systemRoot,
            ["TEMP"] = tempDirectory,
            ["TMP"] = tempDirectory,
            ["ProgramData"] = Environment.GetEnvironmentVariable("ProgramData") ?? @"C:\ProgramData",
            ["ASTERLINK_IPC_IN"] = commandHandle.ToString(System.Globalization.CultureInfo.InvariantCulture),
            ["ASTERLINK_IPC_OUT"] = replyHandle.ToString(System.Globalization.CultureInfo.InvariantCulture),
        };
        var block = string.Concat(variables.Select(kv => kv.Key + "=" + kv.Value + "\0")) + "\0";
        return Marshal.StringToHGlobalUni(block);
    }

    /// <summary>Kills the Core and everything it started, immediately.</summary>
    public void Kill()
    {
        if (disposed) return;
        _ = Native.TerminateJobObject(job, 1);
    }

    public void Dispose()
    {
        lock (gate)
        {
            if (disposed) return;
            disposed = true;
        }
        // Closing the job handle (KILL_ON_JOB_CLOSE) is the guarantee; terminating first just makes it prompt.
        _ = Native.TerminateJobObject(job, 1);
        // Report the exit ourselves: the registered wait may not have fired yet when it is unregistered below.
        _ = Native.WaitForSingleObject(process, 5000);
        _ = Native.GetExitCodeProcess(process, out var code);
        exited.TrySetResult(code);
        waitRegistration.Unregister(null);
        waitEvent.Dispose();
        commands.Dispose();
        replies.Dispose();
        process.Dispose();
        job.Dispose();
    }
}

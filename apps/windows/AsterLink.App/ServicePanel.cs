using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Runtime.CompilerServices;
using System.Windows.Input;
using AsterLink.Platform;
using AsterLink.Shared.Service;

namespace AsterLink.App;

/// <summary>
/// The background service card (users call it "后台服务", handoff D11): what state it is in, the one action that
/// helps, and the outcome of that action. Every failure has its own wording (UAC declined, not installed, would not
/// start, needs repair); no version, code or exception text is shown. Nothing here ever reports a connection as up.
/// </summary>
public sealed class ServicePanel : INotifyPropertyChanged
{
    private readonly InstallLayout? layout = InstallLayout.FromComponentDirectory(AppContext.BaseDirectory);
    private string state = "正在检查后台服务…";
    private string message = "";
    private string detail = "";
    private string actionLabel = "";
    private bool busy;
    private string actionNote = "";
    private RecommendedAction action = RecommendedAction.None;

    public ServicePanel()
    {
        RefreshCommand = new AsyncCommand(RefreshAsync, () => !Busy);
        ActionCommand = new AsyncCommand(RunActionAsync, () => !Busy && action != RecommendedAction.None && action != RecommendedAction.WaitAndRetry);
    }

    public event PropertyChangedEventHandler? PropertyChanged;

    /// <summary>Raised when the service turns from not-ready to ready (after install, repair or start), so the app can carry on with start-up by itself.</summary>
    public event Action? BecameReady;

    /// <summary>True while the GUI's own pipe session is live. A second connection would make the service end that session, so no probe is made then.</summary>
    public Func<bool>? SessionActive { get; set; }

    public bool IsReady { get; private set; }

    public string State { get => state; private set => Set(ref state, value); }
    public bool NotReady => !IsReady;
    public string Message { get => message; private set => Set(ref message, value); }
    public string Detail { get => detail; private set => Set(ref detail, value); }
    public string ActionLabel { get => actionLabel; private set => Set(ref actionLabel, value); }
    public bool HasAction => actionLabel.Length > 0;
    public bool HasMessage => message.Length > 0;
    public bool HasDetail => detail.Length > 0;
    public bool Busy
    {
        get => busy;
        private set { if (Set(ref busy, value)) { RefreshCommand.Raise(); ActionCommand.Raise(); } }
    }

    public AsyncCommand RefreshCommand { get; }
    public AsyncCommand ActionCommand { get; }

    public async Task RefreshAsync()
    {
        if (SessionActive?.Invoke() == true)
        {
            ApplyInUse();
            return;
        }
        Busy = true;
        try { Apply(await ServiceDiagnostics.ProbeAsync(layout).ConfigureAwait(true)); }
        finally { Busy = false; }
    }

    private void ApplyInUse()
    {
        action = RecommendedAction.None;
        SetReady(true);
        State = "后台服务运行正常";
        Message = "";
        Detail = "";
        ActionLabel = "";
        Raise(nameof(HasAction));
        ActionCommand.Raise();
    }

    private void SetReady(bool ready)
    {
        var was = IsReady;
        IsReady = ready;
        if (was != ready) Raise(string.Empty);
        if (ready && !was) BecameReady?.Invoke();
    }

    internal void ApplyPreview(ServiceProbeResult result) => Apply(result);

    private void Apply(ServiceProbeResult result)
    {
        action = result.Action;
        SetReady(result.Connected && result.Health == ServiceHealthState.Ready);
        State = result.Health switch
        {
            ServiceHealthState.Ready => "后台服务运行正常",
            ServiceHealthState.NotInstalled => "首次使用：安装后台服务",
            ServiceHealthState.Stopped => "后台服务未运行",
            ServiceHealthState.Starting => "后台服务正在启动",
            ServiceHealthState.NeedsRepair => "需要修复 AsterLink",
            ServiceHealthState.GuiNotInstalled => "请打开已安装的 AsterLink",
            _ => "后台服务暂时不可用",
        };
        Message = result.Health == ServiceHealthState.Ready ? "" : result.Message;
        // The outcome of the last UAC action stays visible; versions, codes and exception text never reach the screen
        // (the error code is in the copied diagnostics report instead, handoff D9).
        Detail = actionNote;
        ActionLabel = result.Action switch
        {
            RecommendedAction.InstallFromStage => "安装（需要管理员权限）",
            RecommendedAction.Repair => "修复（需要管理员权限）",
            RecommendedAction.StartService => "启动（需要管理员权限）",
            RecommendedAction.LaunchInstalledApp => "打开已安装的 AsterLink",
            _ => "",
        };
        Raise(nameof(HasAction));
        ActionCommand.Raise();
    }

    private async Task RunActionAsync()
    {
        Busy = true;
        actionNote = "";
        try
        {
            var programFiles = InstallVerifier.ProgramFilesLayout();
            switch (action)
            {
                case RecommendedAction.LaunchInstalledApp:
                    Process.Start(new ProcessStartInfo(programFiles.AppExe) { UseShellExecute = true });
                    System.Windows.Application.Current.Shutdown();
                    return;
                case RecommendedAction.InstallFromStage when layout is not null:
                    await ElevateAsync(layout.ServiceExe, ["install", "--stage", layout.Root]).ConfigureAwait(true);
                    break;
                case RecommendedAction.Repair or RecommendedAction.StartService:
                    await ElevateAsync(programFiles.ServiceExe, ["repair"]).ConfigureAwait(true);
                    break;
            }
        }
        finally { Busy = false; }
        await RefreshAsync().ConfigureAwait(true);
    }

    private async Task ElevateAsync(string executable, IReadOnlyList<string> arguments)
    {
        var result = await Elevation.RunAsync(executable, arguments).ConfigureAwait(true);
        actionNote = result.Outcome switch
        {
            Elevation.Outcome.Cancelled => "你取消了授权，没有做任何更改。需要时可以再次点击。",
            Elevation.Outcome.CouldNotStart => "无法开始安装，请稍后重试。",
            _ => result.ExitCode switch
            {
                ServiceConstants.ExitCodes.Ok => "操作已完成。",
                ServiceConstants.ExitCodes.IntegrityFailed => "应用文件不完整，无法自动修复，请从官网重新下载安装。",
                ServiceConstants.ExitCodes.NeedsElevation => "需要管理员权限。",
                _ => "操作未完成，请稍后重试，或从官网重新下载安装。",
            },
        };
    }

    private bool Set<T>(ref T field, T value, [CallerMemberName] string? name = null)
    {
        if (EqualityComparer<T>.Default.Equals(field, value)) return false;
        field = value;
        Raise(name);
        Raise(string.Empty); // the derived Has*/NotReady flags follow
        return true;
    }

    private void Raise(string? name) => PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}

public sealed class AsyncCommand(Func<Task> execute, Func<bool> canExecute) : ICommand
{
    public event EventHandler? CanExecuteChanged;
    public bool CanExecute(object? parameter) => canExecute();
    public async void Execute(object? parameter) => await execute();
    public void Raise() => CanExecuteChanged?.Invoke(this, EventArgs.Empty);
}

using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Runtime.CompilerServices;
using System.Windows.Input;
using AsterLink.Platform;
using AsterLink.Shared.Service;

namespace AsterLink.App;

/// <summary>
/// The "privileged service" card: what state the service is in, the one action that helps, and the
/// outcome of that action. Every failure has its own wording (UAC declined, not installed, would not
/// start, needs repair) and nothing here ever reports a tunnel as up.
/// </summary>
public sealed class ServicePanel : INotifyPropertyChanged
{
    private readonly InstallLayout? layout = InstallLayout.FromComponentDirectory(AppContext.BaseDirectory);
    private string state = "检测中…";
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
    public string Message { get => message; private set => Set(ref message, value); }
    public string Detail { get => detail; private set => Set(ref detail, value); }
    public string ActionLabel { get => actionLabel; private set => Set(ref actionLabel, value); }
    public bool HasAction => actionLabel.Length > 0;
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
        State = "特权服务就绪";
        Message = "服务正在为当前登录使用。修复或重新安装需要先退出登录。";
        Detail = "";
        ActionLabel = "";
        Raise(nameof(HasAction));
        ActionCommand.Raise();
    }

    private void SetReady(bool ready)
    {
        var was = IsReady;
        IsReady = ready;
        if (was != ready) Raise(nameof(IsReady));
        if (ready && !was) BecameReady?.Invoke();
    }

    internal void ApplyPreview(ServiceProbeResult result) => Apply(result);

    private void Apply(ServiceProbeResult result)
    {
        action = result.Action;
        SetReady(result.Connected && result.Health == ServiceHealthState.Ready);
        State = result.Health switch
        {
            ServiceHealthState.Ready => "特权服务就绪",
            ServiceHealthState.NotInstalled => "特权服务未安装",
            ServiceHealthState.Stopped => "特权服务未运行",
            ServiceHealthState.Starting => "特权服务启动中",
            ServiceHealthState.NeedsRepair => "需要修复",
            ServiceHealthState.GuiNotInstalled => "请从安装目录启动",
            _ => "特权服务不可用",
        };
        Message = result.Message;
        var status = result.Connected
            ? $"服务 {result.ServiceVersion} · 内核 {result.CoreVersion} · 渠道 {result.Channel} · 内核状态 {result.SnapshotState}"
            : result.ErrorCode is null ? "" : $"错误代码：{result.ErrorCode}" + (string.IsNullOrEmpty(result.ErrorMessage) ? "" : $"（{result.ErrorMessage}）");
        // The outcome of the last UAC action stays visible next to the fresh status instead of being overwritten.
        Detail = actionNote.Length == 0 ? status : status.Length == 0 ? actionNote : actionNote + "\n" + status;
        ActionLabel = result.Action switch
        {
            RecommendedAction.InstallFromStage => "安装服务（需要管理员授权）",
            RecommendedAction.Repair => "修复服务（需要管理员授权）",
            RecommendedAction.StartService => "启动服务（需要管理员授权）",
            RecommendedAction.LaunchInstalledApp => "打开安装目录中的 AsterLink",
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
            Elevation.Outcome.Cancelled => "已取消管理员授权，没有做任何更改。需要时可以再次点击。",
            Elevation.Outcome.CouldNotStart => "无法启动安装程序：" + result.Detail,
            _ => result.ExitCode switch
            {
                ServiceConstants.ExitCodes.Ok => "管理员操作已完成。",
                ServiceConstants.ExitCodes.IntegrityFailed => "已安装的文件与清单不一致，不能自动修复，请重新安装。",
                ServiceConstants.ExitCodes.NeedsElevation => "需要管理员权限。",
                _ => $"操作失败（退出码 {result.ExitCode}）。",
            },
        };
    }

    private bool Set<T>(ref T field, T value, [CallerMemberName] string? name = null)
    {
        if (EqualityComparer<T>.Default.Equals(field, value)) return false;
        field = value;
        Raise(name);
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

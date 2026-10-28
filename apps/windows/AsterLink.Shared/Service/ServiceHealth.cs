namespace AsterLink.Shared.Service;

public enum ScmState { NotInstalled, Stopped, StartPending, Running, StopPending, Other }

public enum ServiceHealthState
{
    /// <summary>The GUI is not running from a protected install, so the service refuses it by design.</summary>
    GuiNotInstalled,
    NotInstalled,
    Stopped,
    Starting,
    Ready,
    NeedsRepair,
    StartFailed,
}

public enum RecommendedAction { None, InstallFromStage, Repair, StartService, LaunchInstalledApp, WaitAndRetry }

public sealed record ServiceHealth(ServiceHealthState State, RecommendedAction Action, string Message);

/// <summary>
/// Maps measured facts to the one message and the one action the GUI shows (handoff section
/// 9.2: every failure has a visible, specific state). Pure, so each state is a unit test.
/// </summary>
public static class ServiceHealthClassifier
{
    public static ServiceHealth Classify(bool guiInInstalledRoot, bool guiBesideStage, ScmState scm, string? lastErrorCode)
    {
        if (scm == ScmState.NotInstalled)
        {
            return guiBesideStage || guiInInstalledRoot
                ? new(ServiceHealthState.NotInstalled, RecommendedAction.InstallFromStage, "首次使用需要安装后台服务，安装时 Windows 会请求管理员权限。")
                : new(ServiceHealthState.NotInstalled, RecommendedAction.None, "后台服务尚未安装，请从官网下载安装程序重新安装 AsterLink。");
        }

        if (!guiInInstalledRoot)
        {
            return new(ServiceHealthState.GuiNotInstalled, RecommendedAction.LaunchInstalledApp,
                "请从开始菜单打开已安装的 AsterLink。");
        }

        return scm switch
        {
            ScmState.Stopped => new(ServiceHealthState.Stopped, RecommendedAction.StartService, "后台服务未运行，启动时 Windows 会请求管理员权限。"),
            ScmState.StartPending => new(ServiceHealthState.Starting, RecommendedAction.WaitAndRetry, "后台服务正在启动，请稍候。"),
            ScmState.Running => lastErrorCode switch
            {
                ServiceErrors.IntegrityFailed or ServiceErrors.VersionMismatch or ServiceErrors.RecoveryBlocked
                    => new(ServiceHealthState.NeedsRepair, RecommendedAction.Repair, "应用文件或版本不一致，需要修复。修复时 Windows 会请求管理员权限。"),
                ServiceErrors.WorkerUnavailable
                    => new(ServiceHealthState.StartFailed, RecommendedAction.Repair, "暂时无法建立连接，可以尝试修复。"),
                ServiceErrors.PeerRejected or ServiceErrors.OwnerBusy
                    => new(ServiceHealthState.StartFailed, RecommendedAction.None, "另一位 Windows 用户正在使用 AsterLink，请等待其退出后再试。"),
                null => new(ServiceHealthState.Ready, RecommendedAction.None, "后台服务运行正常。"),
                _ => new(ServiceHealthState.StartFailed, RecommendedAction.Repair, "后台服务暂时无法响应，可以尝试修复。"),
            },
            _ => new(ServiceHealthState.Starting, RecommendedAction.WaitAndRetry, "后台服务正在准备，请稍候。"),
        };
    }
}

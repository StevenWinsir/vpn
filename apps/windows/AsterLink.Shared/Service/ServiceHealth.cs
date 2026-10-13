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
                ? new(ServiceHealthState.NotInstalled, RecommendedAction.InstallFromStage, "特权服务尚未安装。需要一次管理员授权来安装（内部测试渠道，未签名）。")
                : new(ServiceHealthState.NotInstalled, RecommendedAction.None, "特权服务尚未安装，且当前程序不在安装包目录中，无法自动安装。");
        }

        if (!guiInInstalledRoot)
        {
            return new(ServiceHealthState.GuiNotInstalled, RecommendedAction.LaunchInstalledApp,
                "服务已安装，但当前程序不是安装目录中的副本，服务会拒绝它。请从安装目录启动 AsterLink。");
        }

        return scm switch
        {
            ScmState.Stopped => new(ServiceHealthState.Stopped, RecommendedAction.StartService, "特权服务未运行。需要管理员授权来启动它。"),
            ScmState.StartPending => new(ServiceHealthState.Starting, RecommendedAction.WaitAndRetry, "特权服务正在启动，请稍候。"),
            ScmState.Running => lastErrorCode switch
            {
                ServiceErrors.IntegrityFailed or ServiceErrors.VersionMismatch or ServiceErrors.RecoveryBlocked
                    => new(ServiceHealthState.NeedsRepair, RecommendedAction.Repair, "安装文件或版本不一致，需要修复。修复需要管理员授权。"),
                ServiceErrors.WorkerUnavailable
                    => new(ServiceHealthState.StartFailed, RecommendedAction.Repair, "代理内核无法启动。可以尝试修复安装。"),
                ServiceErrors.PeerRejected or ServiceErrors.OwnerBusy
                    => new(ServiceHealthState.StartFailed, RecommendedAction.None, "服务拒绝了本次连接（另一位用户正在使用，或程序身份无法确认）。"),
                null => new(ServiceHealthState.Ready, RecommendedAction.None, "特权服务已就绪。"),
                _ => new(ServiceHealthState.StartFailed, RecommendedAction.Repair, "与特权服务通信失败。可以尝试修复安装。"),
            },
            _ => new(ServiceHealthState.Starting, RecommendedAction.WaitAndRetry, "特权服务状态变化中，请稍候。"),
        };
    }
}

import AsterLinkKit
import Foundation
import NetworkExtension

/// The app's `TunnelControl`: the whole connect loop of handoff_app_ios.md 8.6 on top of the
/// round-2 TunnelController.
///
///   connect: VPN configuration (explained and allowed once) -> the app's Core hands the session
///            out -> startVPNTunnel -> the extension adopts, connects and runs Verify stage one ->
///            the system reports connected -> Verify stage two from this process -> only then does
///            the controller show "connected". From that moment every Core request goes to the
///            extension (TunnelBridge).
///   stop:    the extension settles and returns the session, the tunnel is stopped, the app's Core
///            adopts it and the bridge points at the app again. A stop the app did not ask for
///            (Settings, another VPN, the extension ending the tunnel) is noticed by the next
///            request failing, and ends here too.
@MainActor
final class NetworkTunnelControl: TunnelControl {
    private let controller: TunnelController
    private let bridge: TunnelBridge
    private let connectBudget: Duration
    private var stopping = false

    init(controller: TunnelController, bridge: TunnelBridge, connectBudget: Duration = .seconds(90)) {
        self.controller = controller; self.bridge = bridge; self.connectBudget = connectBudget
    }

    var configured: Bool {
        get async {
            if controller.manager == nil { try? await controller.load() }
            return controller.manager != nil
        }
    }

    // MARK: connect

    func connect(generation: UInt64) async -> TunnelOutcome {
        if stopping { return .refused(code: LocalCodes.vpnStopping) }
        if controller.manager == nil { try? await controller.load() }
        if controller.manager == nil || controller.manager?.isEnabled == false {
            do { try await controller.install() } catch {
                let failure = error as NSError
                // The system's "Add VPN Configurations" prompt, answered with Don't Allow.
                let denied = failure.domain == NEVPNErrorDomain && failure.code == NEVPNError.configurationReadWriteFailed.rawValue
                return .refused(code: denied ? LocalCodes.vpnPermissionDenied : LocalCodes.vpnStartFailed)
            }
        }
        do {
            try await controller.connect()
        } catch TunnelController.Failure.core(let failure) {
            return .refused(code: failure.code, message: failure.message)
        } catch {
            return .refused(code: LocalCodes.vpnStartFailed)
        }

        switch await waitUntilConnected() {
        case .up: break
        case .cancelled:
            // The user cancelled (stop() ran): nothing is left to clean up here.
            return .refused(code: LocalCodes.cancelled)
        case .timedOut:
            await stop()
            return .refused(code: LocalCodes.connectTimeout)
        case .down:
            // The extension refused or failed: say why in the Core's words, then take the session
            // back (the extension gives it up without revoking it when it never forwarded).
            let reason = await controller.lastDisconnectNotice()
            _ = await controller.takeBack()
            return .refused(code: reason?.code ?? LocalCodes.vpnStartFailed, message: reason?.text ?? "")
        }

        bridge.useTunnel(ProviderChannel(controller: controller))
        guard let snapshot = try? await CoreClient(transport: bridge).readSnapshot(), snapshot.state == CoreSnapshot.State.connected else {
            await stop()
            return .refused(code: LocalCodes.vpnStartFailed)
        }
        // Verify stage two: this process is an ordinary app, so its request takes the system route.
        let routed = await RouteProbe.run()
        _ = try? await controller.send(.routeProbe(ok: routed))
        guard routed else {
            await stop()
            return .refused(code: LocalCodes.routeProbeFailed)
        }
        return .replied(SnapshotReply(snapshot: snapshot))
    }

    private enum Wait { case up, down, timedOut, cancelled }

    private func waitUntilConnected() async -> Wait {
        let clock = ContinuousClock()
        let deadline = clock.now + connectBudget
        var seenProgress = false
        while clock.now < deadline {
            switch controller.status {
            case .connected: return .up
            case .connecting, .reasserting: seenProgress = true
            case .disconnected, .invalid:
                // A stop() from the cancel button brings the tunnel down without the extension failing.
                if stopping { return .cancelled }
                if seenProgress || !controller.hasHandedOut { return .down }
            case .disconnecting: if stopping { return .cancelled }
            @unknown default: break
            }
            try? await Task.sleep(for: .milliseconds(100))
        }
        return .timedOut
    }

    // MARK: stop

    @discardableResult
    func stop() async -> Bool {
        if stopping { return false }
        let held = bridge.currentRoute == .tunnel
        guard held || controller.isActive || controller.hasHandedOut else { return false }
        stopping = true
        defer { stopping = false }
        if held {
            do {
                // The extension settles the tail and returns the session; the app's Core adopts it.
                _ = try await controller.disconnect()
            } catch {
                // The extension is gone or could not settle: make sure it is down, then try the
                // ticket this process handed out (the server refuses it if the session was revoked).
                await controller.stopAndWait()
                _ = await controller.takeBack()
            }
            bridge.useApp()
        } else {
            await controller.stopAndWait()
            _ = await controller.takeBack()
            bridge.useApp()
        }
        return true
    }

    func foreground(_ visible: Bool) async {
        guard bridge.currentRoute == .tunnel else { return }
        _ = try? await controller.send(.foreground(visible: visible))
    }

    func lastEndReason() async -> Notice? { await controller.lastDisconnectNotice() }

    /// Asked only once the user has seen the VPN work (the setup sheet explains why); a refusal is
    /// reported so the connect page can say reminders will not arrive.
    func notificationsAllowed() async -> Bool { await TunnelNotifier.requestAuthorizationIfNeeded() }

    /// The app was killed or relaunched while the extension kept the tunnel (D-L05). Rejoins a
    /// tunnel that is up and whose Core says connected; closes one that is half-started, since its
    /// session cannot be known from here.
    func resume() async -> Bool {
        if stopping { return false }
        if controller.manager == nil { try? await controller.load() }
        switch controller.status {
        case .connected, .reasserting:
            bridge.useTunnel(ProviderChannel(controller: controller))
            if let snapshot = try? await CoreClient(transport: bridge).readSnapshot(), snapshot.state == CoreSnapshot.State.connected {
                return true
            }
            // Up but not carrying a session: nothing to rejoin.
            bridge.useApp()
            await controller.stopAndWait()
            return false
        case .connecting, .disconnecting:
            await controller.stopAndWait()
            return false
        default:
            return false
        }
    }
}

/// Provider messages as the bridge's channel to the extension's Core.
struct ProviderChannel: ExtensionChannel {
    let controller: TunnelController

    func send(_ request: String, completion: @escaping @Sendable (String?) -> Void) {
        let controller = controller
        Task { @MainActor in completion(try? await controller.call(request)) }
    }
}

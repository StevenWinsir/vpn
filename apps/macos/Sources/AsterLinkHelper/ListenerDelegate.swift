import AsterLinkShared
import Foundation
import SystemConfiguration

/// Accepts exactly one XPC client, from the logged-in console user, whose code
/// signature satisfies the requirement. The system checks the requirement against the
/// connection's audit token; PIDs and process names are never trusted.
final class ListenerDelegate: NSObject, NSXPCListenerDelegate {
    private let requirement: String
    private let supervisor: WorkerSupervisor
    private let helperVersion: String
    private let lock = NSLock()
    private var active: NSXPCConnection?

    init(requirement: String, supervisor: WorkerSupervisor, helperVersion: String) {
        self.requirement = requirement
        self.supervisor = supervisor
        self.helperVersion = helperVersion
    }

    static func consoleUserID() -> uid_t? {
        var uid: uid_t = 0
        guard SCDynamicStoreCopyConsoleUser(nil, &uid, nil) != nil, uid != 0 else { return nil }
        return uid
    }

    func listener(_ listener: NSXPCListener, shouldAcceptNewConnection connection: NSXPCConnection) -> Bool {
        guard let console = ListenerDelegate.consoleUserID(),
              connection.effectiveUserIdentifier == console else { return false }
        lock.lock()
        defer { lock.unlock() }
        guard active == nil else { return false }
        connection.setCodeSigningRequirement(requirement)
        connection.exportedInterface = NSXPCInterface(with: AsterLinkHelperXPC.self)
        connection.exportedObject = HelperService(supervisor: supervisor, helperVersion: helperVersion)
        let release: () -> Void = { [weak self, weak connection] in
            guard let self else { return }
            // No connection, no tunnel: a lost caller never leaves a worker behind.
            self.supervisor.stop()
            self.lock.lock()
            if self.active === connection { self.active = nil }
            self.lock.unlock()
        }
        connection.invalidationHandler = release
        connection.interruptionHandler = release
        active = connection
        connection.resume()
        return true
    }
}

final class HelperService: NSObject, AsterLinkHelperXPC {
    private let supervisor: WorkerSupervisor
    private let helperVersion: String

    init(supervisor: WorkerSupervisor, helperVersion: String) {
        self.supervisor = supervisor
        self.helperVersion = helperVersion
    }

    func hello(reply: @escaping (Data) -> Void) {
        reply(HelperReply(ok: true, helperVersion: helperVersion, workerRunning: supervisor.isRunning).encoded())
    }

    func startWorker(reply: @escaping (Data) -> Void) {
        DispatchQueue.global().async {
            do {
                let hello = try self.supervisor.start()
                reply(HelperReply(ok: true, helperVersion: self.helperVersion, workerRunning: true, core: hello).encoded())
            } catch let failure as HelperFailure {
                reply(HelperReply(ok: false, errorCode: failure.code, detail: failure.detail,
                                  helperVersion: self.helperVersion, workerRunning: self.supervisor.isRunning).encoded())
            } catch {
                reply(HelperReply(ok: false, errorCode: .workerFailed, helperVersion: self.helperVersion,
                                  workerRunning: self.supervisor.isRunning).encoded())
            }
        }
    }

    func stopWorker(reply: @escaping (Data) -> Void) {
        DispatchQueue.global().async {
            self.supervisor.stop()
            reply(HelperReply(ok: true, helperVersion: self.helperVersion, workerRunning: false).encoded())
        }
    }
}

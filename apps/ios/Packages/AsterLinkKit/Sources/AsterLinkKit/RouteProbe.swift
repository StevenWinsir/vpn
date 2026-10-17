import Foundation

/// Verify's second stage (handoff_app_ios.md 8.5). The extension proves the node forwards
/// (its own probe goes through the node in process, because the extension's sockets bypass its
/// tunnel). The app, an ordinary process whose traffic does enter the utun, then proves the
/// system route and DNS: an ephemeral URLSession request to a controlled endpoint must get the
/// exact controlled answer (204 with an empty body), not just any 200 a captive portal or a
/// leak could produce. Both stages must pass before the app shows "connected" (wired in round 4).
public enum RouteProbe {
    public static let endpoints = [
        URL(string: "http://cp.cloudflare.com/generate_204")!,
        URL(string: "http://www.gstatic.com/generate_204")!,
    ]

    /// The controlled answer: status 204 and no body.
    public static func accepts(status: Int, bodyLength: Int) -> Bool { status == 204 && bodyLength == 0 }

    /// Runs the probes in order; true as soon as one gives the controlled answer.
    public static func run(timeout: TimeInterval = 8) async -> Bool {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = timeout
        configuration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        configuration.connectionProxyDictionary = [:] // no proxy: the request must take the system route
        let session = URLSession(configuration: configuration)
        defer { session.invalidateAndCancel() }
        for url in endpoints {
            guard let (data, response) = try? await session.data(from: url), let http = response as? HTTPURLResponse else { continue }
            if accepts(status: http.statusCode, bodyLength: data.count) { return true }
        }
        return false
    }
}

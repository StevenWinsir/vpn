import Foundation

public enum BuildChannel: String, Codable, Sendable {
    case development
    case release
}

public enum SigningPolicyError: Error, Equatable {
    case invalidIdentifier(String)
    case invalidTeamID
    case releaseRequiresTeamID
}

/// Builds the code-signing requirement strings handed to the system (NSXPCConnection
/// `setCodeSigningRequirement`, SecRequirement). The system evaluates them against the
/// peer's audit token; we never compare PIDs or process names ourselves.
public struct SigningPolicy: Sendable, Equatable {
    public let channel: BuildChannel
    public let teamID: String?

    public init(channel: BuildChannel, teamID: String?) throws {
        if let teamID {
            guard SigningPolicy.isValidTeamID(teamID) else { throw SigningPolicyError.invalidTeamID }
        } else if channel == .release {
            throw SigningPolicyError.releaseRequiresTeamID
        }
        self.channel = channel
        self.teamID = teamID
    }

    static func isValidTeamID(_ value: String) -> Bool {
        value.count == 10 && value.allSatisfy { ($0.isASCII && $0.isUppercase) || $0.isNumber && $0.isASCII }
    }

    static func isValidIdentifier(_ value: String) -> Bool {
        !value.isEmpty && value.count <= 128 && value.allSatisfy {
            $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "." || $0 == "-")
        }
    }

    /// Release: Apple-anchored Developer ID chain, exact identifier and Team ID.
    /// Development: identifier only, so ad-hoc local builds work. The development form
    /// is rejected by `requirement` when the channel is release.
    public func requirement(forIdentifier identifier: String) throws -> String {
        guard SigningPolicy.isValidIdentifier(identifier) else { throw SigningPolicyError.invalidIdentifier(identifier) }
        switch channel {
        case .release:
            guard let teamID else { throw SigningPolicyError.releaseRequiresTeamID }
            return "anchor apple generic and identifier \"\(identifier)\" and certificate leaf[subject.OU] = \"\(teamID)\""
        case .development:
            if let teamID {
                return "identifier \"\(identifier)\" and certificate leaf[subject.OU] = \"\(teamID)\""
            }
            return "identifier \"\(identifier)\""
        }
    }
}

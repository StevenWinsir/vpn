import Foundation

/// Fixed identities. These are reviewed constants, not runtime switches: a user
/// editing a file must not be able to repoint the Helper at another caller.
public enum AsterLinkIdentity {
    public static let appBundleID = "com.asterlink.vpn"
    public static let helperBundleID = "com.asterlink.vpn.helper"
    public static let coreIdentifier = "com.asterlink.vpn.core"
    public static let helperMachService = "com.asterlink.vpn.helper"
    public static let helperPlistName = "com.asterlink.vpn.helper.plist"
    /// Independent of the application version (handoff_app.md section 5.3).
    public static let protocolVersion = 1
    public static let maxFrame = 2 << 20
}

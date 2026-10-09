import CryptoKit
import Darwin
import Foundation
import Security

public enum WorkerValidationError: Error, Equatable {
    case symlink(String)
    case notRegularFile
    case wrongOwner(String)
    case writableByOthers(String)
    case hashMismatch
    case tooLarge
    case signatureRejected(Int32)
    case unsafeVersion
}

/// Root-owned worker installation. The Helper never executes a binary from a user
/// controlled location: it copies bytes into a root-owned directory, re-verifies the
/// copy (hash + code signature) and only then launches that copy.
public struct WorkerStore {
    public let root: URL
    /// uid that must own every path component (0 in production; tests pass their own uid).
    public let requiredOwner: uid_t

    public init(root: URL, requiredOwner: uid_t = 0) {
        self.root = root
        self.requiredOwner = requiredOwner
    }

    public static let maxWorkerBytes = 256 << 20

    public static func sha256Hex(of url: URL) throws -> String {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        var hasher = SHA256()
        var total = 0
        while let chunk = try handle.read(upToCount: 1 << 20), !chunk.isEmpty {
            total += chunk.count
            if total > maxWorkerBytes { throw WorkerValidationError.tooLarge }
            hasher.update(data: chunk)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }

    static func isSafeVersion(_ version: String) -> Bool {
        !version.isEmpty && version.count <= 64 && version.allSatisfy {
            $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "." || $0 == "-" || $0 == "+")
        } && !version.hasPrefix(".")
    }

    public func installedURL(version: String) throws -> URL {
        guard WorkerStore.isSafeVersion(version) else { throw WorkerValidationError.unsafeVersion }
        return root.appendingPathComponent(version, isDirectory: true).appendingPathComponent("asterlink-core")
    }

    /// Copies `source` to `<root>/<version>/asterlink-core` atomically after checking its hash.
    @discardableResult
    public func install(source: URL, version: String, expectedSHA256: String) throws -> URL {
        let destination = try installedURL(version: version)
        guard try WorkerStore.sha256Hex(of: source) == expectedSHA256.lowercased() else { throw WorkerValidationError.hashMismatch }
        let directory = destination.deletingLastPathComponent()
        let fm = FileManager.default
        try fm.createDirectory(at: directory, withIntermediateDirectories: true,
                               attributes: [.posixPermissions: 0o755])
        if getuid() == 0 {
            try fm.setAttributes([.ownerAccountID: 0, .groupOwnerAccountID: 0], ofItemAtPath: root.path)
            try fm.setAttributes([.ownerAccountID: 0, .groupOwnerAccountID: 0], ofItemAtPath: directory.path)
        }
        let temporary = directory.appendingPathComponent(".install-\(UUID().uuidString)")
        defer { try? fm.removeItem(at: temporary) }
        try fm.copyItem(at: source, to: temporary)
        try fm.setAttributes([.posixPermissions: 0o755], ofItemAtPath: temporary.path)
        if getuid() == 0 {
            try fm.setAttributes([.ownerAccountID: 0, .groupOwnerAccountID: 0], ofItemAtPath: temporary.path)
        }
        guard try WorkerStore.sha256Hex(of: temporary) == expectedSHA256.lowercased() else { throw WorkerValidationError.hashMismatch }
        if rename(temporary.path, destination.path) != 0 { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
        try validate(destination, expectedSHA256: expectedSHA256)
        return destination
    }

    /// No symlinks anywhere from `root` down; every component owned by `requiredOwner`
    /// and not group/world writable; the file is regular and matches the hash.
    public func validate(_ file: URL, expectedSHA256: String) throws {
        var components: [URL] = []
        var cursor = file.standardizedFileURL
        let rootPath = root.standardizedFileURL.path
        guard cursor.path.hasPrefix(rootPath + "/") else { throw WorkerValidationError.symlink(cursor.path) }
        while cursor.path.count >= rootPath.count {
            components.append(cursor)
            if cursor.path == rootPath { break }
            cursor.deleteLastPathComponent()
        }
        for url in components.reversed() {
            var info = stat()
            guard lstat(url.path, &info) == 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .ENOENT) }
            if (info.st_mode & S_IFMT) == S_IFLNK { throw WorkerValidationError.symlink(url.path) }
            if info.st_uid != requiredOwner { throw WorkerValidationError.wrongOwner(url.path) }
            if info.st_mode & (S_IWGRP | S_IWOTH) != 0 { throw WorkerValidationError.writableByOthers(url.path) }
            if url == file.standardizedFileURL, (info.st_mode & S_IFMT) != S_IFREG { throw WorkerValidationError.notRegularFile }
        }
        guard try WorkerStore.sha256Hex(of: file) == expectedSHA256.lowercased() else { throw WorkerValidationError.hashMismatch }
    }

    /// Static code-signature check of the installed copy against a requirement string.
    public static func verifySignature(_ file: URL, requirement: String) throws {
        var code: SecStaticCode?
        var status = SecStaticCodeCreateWithPath(file as CFURL, [], &code)
        guard status == errSecSuccess, let code else { throw WorkerValidationError.signatureRejected(status) }
        var parsed: SecRequirement?
        status = SecRequirementCreateWithString(requirement as CFString, [], &parsed)
        guard status == errSecSuccess, let parsed else { throw WorkerValidationError.signatureRejected(status) }
        status = SecStaticCodeCheckValidityWithErrors(code, SecCSFlags(rawValue: kSecCSCheckAllArchitectures | kSecCSStrictValidate), parsed, nil)
        guard status == errSecSuccess else { throw WorkerValidationError.signatureRejected(status) }
    }
}

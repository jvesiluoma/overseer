import Foundation

public enum PairingResult: Equatable, Sendable {
    case success(token: String)
    case invalid(remainingAttempts: Int)
    case lockedOut(retryAfter: Double)
}

/// Issues session tokens in exchange for the short code shown in the host's menu bar.
///
/// Brute-force defences: per-client lockout with exponential backoff, a global failure budget
/// that rotates the code, rotation after every successful pairing, and constant-time comparison.
public final class PairingManager: @unchecked Sendable {
    public struct Configuration: Sendable {
        public var codeLength = 6
        public var maxFailuresPerClient = 5
        public var baseLockout: Double = 30
        public var maxLockout: Double = 15 * 60
        /// Failures from all clients within `globalWindow` that force a new code.
        public var globalFailureBudget = 20
        public var globalWindow: Double = 60
        public var maxTokens = 32

        public init() {}
    }

    private struct ClientRecord {
        var failures = 0
        var lockouts = 0
        var lockedUntil: Double = 0
    }

    private let config: Configuration
    private let clock: () -> Double
    private let lock = NSLock()
    private var code: String
    private var clients: [String: ClientRecord] = [:]
    private var globalFailures: [Double] = []
    private var tokens: [String] = []

    /// Called (outside the lock) whenever the code changes so the UI can show the new one.
    public var onCodeChange: (@Sendable (String) -> Void)?

    public init(configuration: Configuration = Configuration(),
                clock: @escaping () -> Double = { ProcessInfo.processInfo.systemUptime }) {
        self.config = configuration
        self.clock = clock
        self.code = PairingManager.makeCode(length: configuration.codeLength)
    }

    public var currentCode: String {
        lock.lock(); defer { lock.unlock() }
        return code
    }

    public func attempt(code candidate: String, client: String) -> PairingResult {
        let now = clock()
        var rotated: String?
        let result: PairingResult = lock.sync {
            var record = clients[client] ?? ClientRecord()
            if record.lockedUntil > now {
                return .lockedOut(retryAfter: record.lockedUntil - now)
            }
            if PairingManager.constantTimeEquals(candidate, code) {
                clients[client] = nil
                let token = PairingManager.makeToken()
                tokens.append(token)
                if tokens.count > config.maxTokens { tokens.removeFirst(tokens.count - config.maxTokens) }
                code = PairingManager.makeCode(length: config.codeLength)
                rotated = code
                return .success(token: token)
            }
            record.failures += 1
            globalFailures.append(now)
            globalFailures.removeAll { now - $0 > config.globalWindow }
            if globalFailures.count >= config.globalFailureBudget {
                globalFailures.removeAll()
                code = PairingManager.makeCode(length: config.codeLength)
                rotated = code
            }
            if record.failures >= config.maxFailuresPerClient {
                record.failures = 0
                record.lockouts += 1
                let duration = min(config.baseLockout * pow(2, Double(record.lockouts - 1)), config.maxLockout)
                record.lockedUntil = now + duration
                clients[client] = record
                return .lockedOut(retryAfter: duration)
            }
            clients[client] = record
            return .invalid(remainingAttempts: config.maxFailuresPerClient - record.failures)
        }
        if let rotated { onCodeChange?(rotated) }
        return result
    }

    /// Issues a token without a code; used only when the user turned pairing off.
    public func issueUnauthenticatedToken() -> String {
        lock.sync {
            let token = PairingManager.makeToken()
            tokens.append(token)
            if tokens.count > config.maxTokens { tokens.removeFirst(tokens.count - config.maxTokens) }
            return token
        }
    }

    public func isValid(token: String) -> Bool {
        guard token.count == 32 else { return false }
        return lock.sync { tokens.contains { PairingManager.constantTimeEquals($0, token) } }
    }

    public func revokeAll() {
        let newCode: String = lock.sync {
            tokens.removeAll()
            code = PairingManager.makeCode(length: config.codeLength)
            return code
        }
        onCodeChange?(newCode)
    }

    public static func constantTimeEquals(_ a: String, _ b: String) -> Bool {
        let x = Array(a.utf8), y = Array(b.utf8)
        var diff = UInt8(x.count == y.count ? 0 : 1)
        for i in 0..<max(x.count, y.count) {
            let l = i < x.count ? x[i] : 0
            let r = i < y.count ? y[i] : 0
            diff |= l ^ r
        }
        return diff == 0
    }

    static func makeCode(length: Int) -> String {
        var rng = SystemRandomNumberGenerator()
        return String((0..<length).map { _ in Character(String(Int.random(in: 0...9, using: &rng))) })
    }

    static func makeToken() -> String {
        var rng = SystemRandomNumberGenerator()
        return (0..<16).map { _ in String(format: "%02x", UInt8.random(in: 0...255, using: &rng)) }.joined()
    }
}

extension NSLock {
    /// Runs `body` while holding the lock (NSLocking.withLock is not on every supported platform).
    @inlinable
    public func sync<T>(_ body: () throws -> T) rethrows -> T {
        lock()
        defer { unlock() }
        return try body()
    }
}

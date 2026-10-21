import Foundation
import OverscreenCore

/// Shared, thread-safe state the HTTP and signaling handlers read from event-loop threads.
public final class ServerContext: @unchecked Sendable {
    public let pairing: PairingManager
    public let hub: SessionHub
    public let appVersion: String
    public let hostName: String

    private let lock = NSLock()
    private var _settings: HostSettings

    public init(settings: HostSettings, pairing: PairingManager = PairingManager(),
                hub: SessionHub = SessionHub(), appVersion: String, hostName: String) {
        self._settings = settings.sanitized()
        self.pairing = pairing
        self.hub = hub
        self.appVersion = appVersion
        self.hostName = hostName
    }

    public var settings: HostSettings {
        get { lock.sync { _settings } }
        set { lock.sync { _settings = newValue.sanitized() } }
    }
}

public struct ConnectedClient: Sendable, Identifiable, Equatable {
    public var id: String
    public var displayIndex: Int
    public var remoteAddress: String
    public var connectedAt: Date
    public var latencyMs: Int?
    public var stats: ClientStats?
}

/// Tracks live sessions; at most one per display (a new connection replaces the old one).
public final class SessionHub: @unchecked Sendable {
    private struct Entry {
        var info: ConnectedClient
        var kick: @Sendable (String) -> Void
    }

    private let lock = NSLock()
    private var entries: [String: Entry] = [:]

    /// Called with the current client list after every change. Invoked outside the lock.
    public var onChange: (@Sendable ([ConnectedClient]) -> Void)?

    public init() {}

    public var clients: [ConnectedClient] {
        lock.sync { entries.values.map(\.info).sorted { $0.displayIndex < $1.displayIndex } }
    }

    public var count: Int { lock.sync { entries.count } }

    public func isInUse(display: Int) -> Bool {
        lock.sync { entries.values.contains { $0.info.displayIndex == display } }
    }

    /// Registers a session; returns the number of live sessions afterwards.
    /// A previous session on the same display is kicked with reason "replaced".
    @discardableResult
    public func register(_ client: ConnectedClient, kick: @escaping @Sendable (String) -> Void) -> Int {
        let (evicted, total): ([Entry], Int) = lock.sync {
            let old = entries.values.filter { $0.info.displayIndex == client.displayIndex && $0.info.id != client.id }
            for e in old { entries[e.info.id] = nil }
            entries[client.id] = Entry(info: client, kick: kick)
            return (old, entries.count)
        }
        for e in evicted { e.kick("replaced") }
        notify()
        return total
    }

    public func unregister(_ id: String) {
        let removed = lock.sync { entries.removeValue(forKey: id) != nil }
        if removed { notify() }
    }

    public func update(_ id: String, latencyMs: Int? = nil, stats: ClientStats? = nil) {
        let changed: Bool = lock.sync {
            guard var e = entries[id] else { return false }
            if let latencyMs { e.info.latencyMs = latencyMs }
            if let stats { e.info.stats = stats }
            entries[id] = e
            return true
        }
        if changed { notify() }
    }

    public func disconnectAll(reason: String = "host_stopped") {
        let all: [Entry] = lock.sync {
            let v = Array(entries.values)
            entries.removeAll()
            return v
        }
        for e in all { e.kick(reason) }
        notify()
    }

    private func notify() {
        onChange?(clients)
    }
}

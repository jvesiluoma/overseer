import AppKit
import Combine
import Foundation
import OverscreenCore
import OverscreenServer
import ServiceManagement

/// Owns the server and media engine; the menu bar UI observes it.
@MainActor
public final class HostController: ObservableObject {
    public enum State: Equatable {
        case stopped
        case running(port: Int)
        case failed(String)
    }

    @Published public private(set) var state: State = .stopped
    @Published public private(set) var urls: [String] = []
    @Published public private(set) var pairingCode = ""
    @Published public private(set) var clients: [ConnectedClient] = []
    @Published public private(set) var hasScreenRecording = Permissions.hasScreenRecording
    @Published public private(set) var hasAccessibility = Permissions.hasAccessibility
    /// SHA-256 of the TLS certificate, for comparing with what the browser shows.
    @Published public private(set) var certificateFingerprint = ""
    /// The Mac's Internet Sharing hotspot or the phone hotspot it has joined, if any.
    @Published public private(set) var hotspot: Hotspot?
    @Published public var settings: HostSettings {
        didSet {
            // Assigning a @Published property re-enters didSet, so only reassign when sanitizing
            // changes something; the nested call then does the saving.
            let sanitized = settings.sanitized()
            guard sanitized == settings else { settings = sanitized; return }
            guard settings != oldValue else { return }
            SettingsStore.save(settings)
            context.settings = settings
            engine?.settings = settings
        }
    }

    public var isRunning: Bool {
        if case .running = state { return true }
        return false
    }

    public let version: String
    private let context: ServerContext
    private var engine: MacMediaEngine?
    private var server: OverscreenHTTPServer?
    /// Plain HTTP listener: redirects to HTTPS, or serves everything if insecure HTTP is allowed.
    private var plainServer: OverscreenHTTPServer?
    private var plainPort: Int?
    private var permissionTimer: Timer?

    public init() {
        let settings = SettingsStore.load()
        self.settings = settings
        self.version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"
        let pairing = PairingManager()
        context = ServerContext(settings: settings, pairing: pairing, appVersion: version,
                                hostName: Host.current().localizedName ?? "Mac")
        pairingCode = pairing.currentCode
        pairing.onCodeChange = { [weak self] code in
            Task { @MainActor in self?.pairingCode = code }
        }
        context.hub.onChange = { [weak self] clients in
            Task { @MainActor in self?.clients = clients }
        }
        // Permissions can be granted in System Settings at any time.
        permissionTimer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refreshPermissions() }
        }
    }

    public func start() {
        guard !isRunning else { return }
        refreshPermissions()
        if !hasScreenRecording {
            Permissions.requestScreenRecording()
        }
        if settings.allowInput && !hasAccessibility {
            Permissions.requestAccessibility()
        }
        let engine = self.engine ?? MacMediaEngine(settings: settings)
        self.engine = engine
        let identity: TLSIdentity
        do {
            identity = try Self.loadTLSIdentity()
        } catch {
            state = .failed("Could not create the TLS certificate: \(error)")
            return
        }
        certificateFingerprint = identity.fingerprint
        do {
            let server = try OverscreenHTTPServer(context: context, engine: engine, transport: .tls(identity))
            let port = try server.start(ports: [settings.httpsPort, settings.httpsFallbackPort])
            self.server = server
            state = .running(port: port)
            startPlainServer(engine: engine, httpsPort: port)
            urls = connectURLs(port: port, addresses: NetworkInterfaces.ipv4Addresses())
        } catch {
            state = .failed("Could not open port \(settings.httpsPort) or \(settings.httpsFallbackPort): \(error)")
        }
    }

    /// Optional: if the HTTP ports are taken, HTTPS still works when the URL is typed in full.
    private func startPlainServer(engine: MacMediaEngine, httpsPort: Int) {
        let transport: ServerTransport = settings.allowInsecureHTTP ? .plain : .redirectToHTTPS(port: httpsPort)
        do {
            let plain = try OverscreenHTTPServer(context: context, engine: engine, transport: transport)
            plainPort = try plain.start(ports: [settings.port, settings.fallbackPort])
            plainServer = plain
        } catch {
            NSLog("Overscreen: plain HTTP listener not started: \(error)")
        }
    }

    public func stop() {
        server?.stop()
        server = nil
        plainServer?.stop()
        plainServer = nil
        plainPort = nil
        state = .stopped
        urls = []
        let engine = self.engine
        Task { await engine?.shutdown() }
    }

    public func revokeAllDevices() {
        context.pairing.revokeAll()
        context.hub.disconnectAll(reason: "revoked")
    }

    /// Also picks up network changes, such as Internet Sharing being switched on.
    public func refreshPermissions() {
        hasScreenRecording = Permissions.hasScreenRecording
        hasAccessibility = Permissions.hasAccessibility
        let addresses = NetworkInterfaces.ipv4Addresses()
        let hotspot = NetworkInterfaces.hotspot(in: addresses)
        if hotspot != self.hotspot { self.hotspot = hotspot }
        if case .running(let port) = state {
            let urls = connectURLs(port: port, addresses: addresses)
            if urls != self.urls { self.urls = urls }
        }
    }

    public var launchAtLogin: Bool {
        get { SMAppService.mainApp.status == .enabled }
        set {
            do {
                if newValue { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            } catch {
                NSLog("Overscreen: launch at login change failed: \(error)")
            }
            objectWillChange.send()
        }
    }

    private func connectURLs(port: Int, addresses: [InterfaceAddress]) -> [String] {
        var result = Self.urls(scheme: "https", port: port, defaultPort: 443, addresses: addresses)
        if settings.allowInsecureHTTP, let plainPort {
            result += Self.urls(scheme: "http", port: plainPort, defaultPort: 80, addresses: addresses)
        }
        return result
    }

    private static func urls(scheme: String, port: Int, defaultPort: Int, addresses: [InterfaceAddress]) -> [String] {
        let suffix = port == defaultPort ? "" : ":\(port)"
        var result = addresses.map { "\(scheme)://\($0.address)\(suffix)" }
        if let name = Host.current().name, name.hasSuffix(".local") { result.append("\(scheme)://\(name)\(suffix)") }
        return result
    }

    /// Kept in Application Support so the fingerprint, and the browser exceptions, survive restarts.
    private static func loadTLSIdentity() throws -> TLSIdentity {
        let support = try FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask,
                                                  appropriateFor: nil, create: true)
        var dnsNames = ["localhost"]
        if let name = Host.current().name, name.hasSuffix(".local") { dnsNames.append(name) }
        return try TLSIdentity.loadOrCreate(directory: support.appendingPathComponent("Overscreen", isDirectory: true),
                                            commonName: "Overscreen on \(Host.current().localizedName ?? "Mac")",
                                            dnsNames: dnsNames,
                                            ipAddresses: [NetworkInterfaces.internetSharingDefaultAddress]
                                                + NetworkInterfaces.ipv4Addresses().map(\.address))
    }
}

enum SettingsStore {
    private static let key = "org.overscreen.settings.v1"

    static func load() -> HostSettings {
        guard let data = UserDefaults.standard.data(forKey: key),
              let settings = try? JSONDecoder().decode(HostSettings.self, from: data)
        else { return HostSettings() }
        return settings.sanitized()
    }

    static func save(_ settings: HostSettings) {
        if let data = try? JSONEncoder().encode(settings) {
            UserDefaults.standard.set(data, forKey: key)
        }
    }
}

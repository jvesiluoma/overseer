import Foundation

public enum ResolutionPolicy: Codable, Equatable, Sendable {
    /// Match the browser's viewport (CSS px x devicePixelRatio).
    case auto
    case custom(width: Int, height: Int)
}

public struct DisplayConfig: Codable, Equatable, Sendable, Identifiable {
    public var id: Int
    public var name: String
    public var resolution: ResolutionPolicy
    /// Retina-style rendering: macOS draws at 2x, so UI appears at half the pixel size.
    public var hiDPI: Bool

    public init(id: Int, name: String, resolution: ResolutionPolicy = .auto, hiDPI: Bool = false) {
        self.id = id
        self.name = name
        self.resolution = resolution
        self.hiDPI = hiDPI
    }
}

public struct HostSettings: Codable, Equatable, Sendable {
    /// Encrypted listener for the web client, pairing and signaling.
    public var httpsPort: Int = 443
    public var httpsFallbackPort: Int = 8443
    /// Unencrypted listener. It only redirects to HTTPS unless `allowInsecureHTTP` is on.
    public var port: Int = 80
    public var fallbackPort: Int = 8080
    /// Serve the full client over plain HTTP too, for browsers that refuse self-signed
    /// certificates. Anyone on the network can then read the pairing code and session token.
    public var allowInsecureHTTP = false
    public var requirePairing = true
    public var allowInput = true
    public var invertScroll = false
    public var fps = 30
    public var maxBitrateMbps = 8.0
    public var displays: [DisplayConfig] = [DisplayConfig(id: 0, name: "Display 1")]
    /// Extra host names accepted in the Host header (besides IP literals, localhost and *.local).
    public var extraHostNames: [String] = ["go.ss"]

    public init() {}

    /// Missing keys (settings saved by an older version) keep their defaults instead of
    /// failing the whole decode.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let d = HostSettings()
        httpsPort = try c.decodeIfPresent(Int.self, forKey: .httpsPort) ?? d.httpsPort
        httpsFallbackPort = try c.decodeIfPresent(Int.self, forKey: .httpsFallbackPort) ?? d.httpsFallbackPort
        port = try c.decodeIfPresent(Int.self, forKey: .port) ?? d.port
        fallbackPort = try c.decodeIfPresent(Int.self, forKey: .fallbackPort) ?? d.fallbackPort
        allowInsecureHTTP = try c.decodeIfPresent(Bool.self, forKey: .allowInsecureHTTP) ?? d.allowInsecureHTTP
        requirePairing = try c.decodeIfPresent(Bool.self, forKey: .requirePairing) ?? d.requirePairing
        allowInput = try c.decodeIfPresent(Bool.self, forKey: .allowInput) ?? d.allowInput
        invertScroll = try c.decodeIfPresent(Bool.self, forKey: .invertScroll) ?? d.invertScroll
        fps = try c.decodeIfPresent(Int.self, forKey: .fps) ?? d.fps
        maxBitrateMbps = try c.decodeIfPresent(Double.self, forKey: .maxBitrateMbps) ?? d.maxBitrateMbps
        displays = try c.decodeIfPresent([DisplayConfig].self, forKey: .displays) ?? d.displays
        extraHostNames = try c.decodeIfPresent([String].self, forKey: .extraHostNames) ?? d.extraHostNames
    }

    public static let supportedFps = [24, 30, 60]
    public static let maxDisplays = 4

    /// Clamps values that may come from a hand-edited or older settings file.
    public func sanitized() -> HostSettings {
        var s = self
        if !(1...65535).contains(s.httpsPort) { s.httpsPort = 443 }
        if !(1...65535).contains(s.httpsFallbackPort) { s.httpsFallbackPort = 8443 }
        if !(1...65535).contains(s.port) { s.port = 80 }
        if !(1...65535).contains(s.fallbackPort) { s.fallbackPort = 8080 }
        if !HostSettings.supportedFps.contains(s.fps) { s.fps = 30 }
        s.maxBitrateMbps = min(max(s.maxBitrateMbps, 1), 50)
        if s.displays.isEmpty { s.displays = [DisplayConfig(id: 0, name: "Display 1")] }
        s.displays = Array(s.displays.prefix(HostSettings.maxDisplays))
        for i in s.displays.indices { s.displays[i].id = i }
        return s
    }
}

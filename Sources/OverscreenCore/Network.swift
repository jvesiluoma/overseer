import Foundation
#if canImport(Darwin)
import Darwin
#else
import Glibc
#endif

public struct InterfaceAddress: Equatable, Sendable {
    public var interface: String
    public var address: String
}

/// How the device can reach the Mac when it is not simply on the same Wi-Fi.
public enum HotspotKind: Equatable, Sendable {
    /// The Mac shares a phone's (USB) or wired connection over Wi-Fi with macOS Internet Sharing.
    case internetSharing
    /// The Mac has joined an iPhone's Personal Hotspot; the device must join the same hotspot.
    case iPhoneHotspot
}

public struct Hotspot: Equatable, Sendable {
    public var kind: HotspotKind
    public var address: InterfaceAddress
}

public enum NetworkInterfaces {
    /// Address macOS Internet Sharing gives the Mac by default.
    public static let internetSharingDefaultAddress = "192.168.2.1"

    /// IPv4 addresses of interfaces that are up, excluding loopback: hotspot addresses first,
    /// then other private ranges.
    public static func ipv4Addresses() -> [InterfaceAddress] {
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let first = head else { return [] }
        defer { freeifaddrs(head) }
        var result: [InterfaceAddress] = []
        var cursor: UnsafeMutablePointer<ifaddrs>? = first
        while let entry = cursor {
            defer { cursor = entry.pointee.ifa_next }
            let flags = Int32(bitPattern: UInt32(entry.pointee.ifa_flags))
            guard flags & Int32(IFF_UP) != 0, flags & Int32(IFF_LOOPBACK) == 0,
                  let addr = entry.pointee.ifa_addr, addr.pointee.sa_family == sa_family_t(AF_INET)
            else { continue }
            var buffer = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
            let ok = addr.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { sin -> Bool in
                var a = sin.pointee.sin_addr
                return inet_ntop(AF_INET, &a, &buffer, socklen_t(INET_ADDRSTRLEN)) != nil
            }
            guard ok else { continue }
            let address = String(decoding: buffer.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) }, as: UTF8.self)
            result.append(InterfaceAddress(interface: String(cString: entry.pointee.ifa_name), address: address))
        }
        return sorted(result)
    }

    /// Stable sort: hotspot addresses first, then private, public and link-local ones.
    public static func sorted(_ addresses: [InterfaceAddress]) -> [InterfaceAddress] {
        addresses.enumerated().sorted { a, b in
            let ra = rank(a.element), rb = rank(b.element)
            return ra != rb ? ra < rb : a.offset < b.offset
        }.map(\.element)
    }

    static func rank(_ a: InterfaceAddress) -> Int {
        if hotspotKind(of: a) != nil { return 0 }
        if isPrivateIPv4(a.address) { return 1 }
        if a.address.hasPrefix("169.254.") { return 3 }
        return 2
    }

    /// The hotspot the Mac is part of, if any. Internet Sharing wins over a phone hotspot,
    /// because then the device joins the Mac's own network.
    public static func hotspot(in addresses: [InterfaceAddress]) -> Hotspot? {
        let found = addresses.compactMap { a in hotspotKind(of: a).map { Hotspot(kind: $0, address: a) } }
        return found.first { $0.kind == .internetSharing } ?? found.first
    }

    static func hotspotKind(of a: InterfaceAddress) -> HotspotKind? {
        if isInternetSharingBridge(a.interface) { return .internetSharing }
        if isIPhoneHotspotAddress(a.address) { return .iPhoneHotspot }
        return nil
    }

    /// Internet Sharing creates bridge100, bridge101, …; bridge0 is the Thunderbolt Bridge.
    static func isInternetSharingBridge(_ name: String) -> Bool {
        guard name.hasPrefix("bridge"), let n = Int(name.dropFirst("bridge".count)) else { return false }
        return n >= 100
    }

    /// iPhone Personal Hotspot hands out 172.20.10.2–14 (172.20.10.0/28, gateway .1).
    static func isIPhoneHotspotAddress(_ address: String) -> Bool {
        let parts = address.split(separator: ".").compactMap { Int($0) }
        return parts.count == 4 && parts[0] == 172 && parts[1] == 20 && parts[2] == 10 && (2...14).contains(parts[3])
    }

    public static func isPrivateIPv4(_ address: String) -> Bool {
        let parts = address.split(separator: ".").compactMap { Int($0) }
        guard parts.count == 4 else { return false }
        switch (parts[0], parts[1]) {
        case (10, _), (192, 168): return true
        case (172, 16...31): return true
        default: return false
        }
    }
}

/// Defends the local HTTP server against DNS rebinding: a page on an attacker's domain that
/// re-resolves to the host's LAN IP still sends its own domain in `Host`, which we reject.
public enum HostHeaderPolicy {
    public static func isAllowed(host rawHost: String, extraNames: [String]) -> Bool {
        let host = hostname(from: rawHost).lowercased()
        guard !host.isEmpty else { return false }
        if host == "localhost" || host.hasSuffix(".local") { return true }
        if isIPv4Literal(host) || host.contains(":") { return true } // IPv6 literal (brackets removed)
        return extraNames.contains { $0.lowercased() == host }
    }

    /// The browser must not open our WebSocket from another origin (WebSockets ignore CORS).
    public static func originMatchesHost(origin: String?, host: String?) -> Bool {
        guard let origin, let host else { return origin == nil }
        guard let url = URL(string: origin), let scheme = url.scheme, scheme == "http" || scheme == "https",
              let originHost = url.host else { return false }
        let originAuthority = url.port.map { "\(originHost):\($0)" } ?? originHost
        func normalize(_ s: String) -> String {
            var s = s.lowercased()
            if s.hasPrefix("[") { s = s.replacingOccurrences(of: "[", with: "").replacingOccurrences(of: "]", with: "") }
            let defaultPort = scheme == "https" ? ":443" : ":80"
            if s.hasSuffix(defaultPort) { s.removeLast(defaultPort.count) }
            return s
        }
        return normalize(originAuthority) == normalize(host)
    }

    public static func hostname(from raw: String) -> String {
        let trimmed = raw.trimmingCharacters(in: .whitespaces)
        if trimmed.hasPrefix("[") {
            guard let end = trimmed.firstIndex(of: "]") else { return "" }
            return String(trimmed[trimmed.index(after: trimmed.startIndex)..<end])
        }
        if let colon = trimmed.firstIndex(of: ":") { return String(trimmed[..<colon]) }
        return trimmed
    }

    static func isIPv4Literal(_ s: String) -> Bool {
        let parts = s.split(separator: ".", omittingEmptySubsequences: false)
        return parts.count == 4 && parts.allSatisfy { p in
            !p.isEmpty && p.count <= 3 && p.allSatisfy(\.isNumber) && (Int(p) ?? 256) <= 255
        }
    }
}

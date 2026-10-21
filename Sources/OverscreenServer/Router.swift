import Foundation
import OverscreenCore

public struct HTTPRequestInfo: Sendable {
    public var method: String
    /// Request target as sent (path + optional query).
    public var uri: String
    public var headers: [(String, String)]
    public var body: Data
    public var remoteAddress: String

    public init(method: String, uri: String, headers: [(String, String)] = [], body: Data = Data(), remoteAddress: String = "127.0.0.1") {
        self.method = method
        self.uri = uri
        self.headers = headers
        self.body = body
        self.remoteAddress = remoteAddress
    }

    public func header(_ name: String) -> String? {
        headers.first { $0.0.caseInsensitiveCompare(name) == .orderedSame }?.1
    }

    public var path: String { RequestTarget.path(of: uri) }
    public var query: [String: String] { RequestTarget.query(of: uri) }
}

public struct HTTPResponseInfo: Sendable {
    public var status: Int
    public var headers: [(String, String)]
    public var body: Data

    public init(status: Int, headers: [(String, String)] = [], body: Data = Data()) {
        self.status = status
        self.headers = headers
        self.body = body
    }

    public func header(_ name: String) -> String? {
        headers.first { $0.0.caseInsensitiveCompare(name) == .orderedSame }?.1
    }
}

enum RequestTarget {
    static func path(of uri: String) -> String {
        let p = uri.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? "/"
        return p.isEmpty ? "/" : p
    }

    static func query(of uri: String) -> [String: String] {
        let parts = uri.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: false)
        guard parts.count == 2 else { return [:] }
        var result: [String: String] = [:]
        for pair in parts[1].split(separator: "&") {
            let kv = pair.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false)
            guard let key = kv.first?.removingPercentEncoding else { continue }
            let value = kv.count > 1 ? (String(kv[1]).replacingOccurrences(of: "+", with: " ").removingPercentEncoding ?? "") : ""
            if result[key] == nil { result[key] = value }
        }
        return result
    }
}

/// How a listener talks to browsers.
public enum ServerTransport: Sendable {
    /// Unencrypted: the pairing code, token and signaling can be read on the network.
    case plain
    case tls(TLSIdentity)
    /// Unencrypted listener that only sends browsers to the HTTPS port; it serves no API.
    case redirectToHTTPS(port: Int)
}

/// Pure request -> response logic, independent of SwiftNIO so it can be unit tested.
public final class Router: Sendable {
    public static let maxBodyBytes = 4096

    private let context: ServerContext
    private let transport: ServerTransport

    public init(context: ServerContext, transport: ServerTransport = .plain) {
        self.context = context
        self.transport = transport
    }

    public func handle(_ request: HTTPRequestInfo) -> HTTPResponseInfo {
        var response = route(request)
        response.headers.append(contentsOf: Router.securityHeaders)
        if request.method == "HEAD" { response.body = Data() }
        return response
    }

    /// Decides whether a WebSocket upgrade request may proceed.
    public func authorizeWebSocket(_ request: HTTPRequestInfo) -> Bool {
        if case .redirectToHTTPS = transport { return false }
        guard request.path == "/ws", request.method == "GET" else { return false }
        guard let host = request.header("Host"),
              HostHeaderPolicy.isAllowed(host: host, extraNames: context.settings.extraHostNames),
              HostHeaderPolicy.originMatchesHost(origin: request.header("Origin"), host: host)
        else { return false }
        guard let token = request.query["token"] else { return false }
        return context.pairing.isValid(token: token)
    }

    static let securityHeaders: [(String, String)] = [
        ("Cache-Control", "no-store"),
        ("X-Content-Type-Options", "nosniff"),
        ("X-Frame-Options", "DENY"),
        ("Referrer-Policy", "no-referrer"),
        ("Content-Security-Policy",
         "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; media-src 'self' blob: mediastream:; "
            + "style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"),
    ]

    private func route(_ request: HTTPRequestInfo) -> HTTPResponseInfo {
        guard let host = request.header("Host"),
              HostHeaderPolicy.isAllowed(host: host, extraNames: context.settings.extraHostNames)
        else {
            return text(421, "Misdirected request")
        }
        if case .redirectToHTTPS(let port) = transport {
            return redirect(host: host, httpsPort: port, uri: request.uri)
        }
        let path = request.path
        if path.hasPrefix("/api/") {
            return api(request, path: path)
        }
        if path == "/ws" {
            // Reaching the router means the upgrade was refused (bad token, origin or not an upgrade).
            return json(401, ["error": "unauthorized"])
        }
        guard request.method == "GET" || request.method == "HEAD" else {
            return text(405, "Method not allowed", extra: [("Allow", "GET, HEAD")])
        }
        let name = path == "/" ? "index.html" : String(path.dropFirst())
        // Assets come from a fixed in-memory table: no filesystem access, no traversal.
        guard let asset = WebAssets.files[name] else {
            return text(404, "Not found")
        }
        return HTTPResponseInfo(status: 200, headers: [("Content-Type", asset.contentType)], body: Data(asset.body.utf8))
    }

    private func api(_ request: HTTPRequestInfo, path: String) -> HTTPResponseInfo {
        switch (request.method, path) {
        case ("GET", "/api/info"), ("HEAD", "/api/info"):
            let settings = context.settings
            let displays = settings.displays.map { d -> [String: Any] in
                ["id": d.id, "name": d.name, "inUse": context.hub.isInUse(display: d.id)]
            }
            return json(200, [
                "name": context.hostName,
                "version": context.appVersion,
                "protocol": overscreenProtocolVersion,
                "pairingRequired": settings.requirePairing,
                "displays": displays,
            ])
        case ("POST", "/api/pair"):
            return pair(request)
        case ("GET", "/api/auth"):
            guard let auth = request.header("Authorization"), auth.hasPrefix("Bearer "),
                  context.pairing.isValid(token: String(auth.dropFirst(7)))
            else { return json(401, ["error": "unauthorized"]) }
            return HTTPResponseInfo(status: 204)
        case (_, "/api/info"), (_, "/api/pair"), (_, "/api/auth"):
            return json(405, ["error": "method_not_allowed"])
        default:
            return json(404, ["error": "not_found"])
        }
    }

    private func pair(_ request: HTTPRequestInfo) -> HTTPResponseInfo {
        // Requiring JSON makes this a non-simple CORS request, so other sites cannot post blindly.
        guard let type = request.header("Content-Type"), type.lowercased().hasPrefix("application/json") else {
            return json(415, ["error": "unsupported_media_type"])
        }
        guard request.body.count <= Router.maxBodyBytes else { return json(413, ["error": "too_large"]) }
        if !context.settings.requirePairing {
            return json(200, ["token": context.pairing.issueUnauthenticatedToken()])
        }
        guard let object = try? JSONSerialization.jsonObject(with: request.body) as? [String: Any],
              let code = object["code"] as? String, code.count <= 16
        else { return json(400, ["error": "bad_request"]) }
        switch context.pairing.attempt(code: code, client: request.remoteAddress) {
        case .success(let token):
            return json(200, ["token": token])
        case .invalid(let remaining):
            return json(401, ["error": "invalid_code", "remainingAttempts": remaining])
        case .lockedOut(let retryAfter):
            let seconds = Int(retryAfter.rounded(.up))
            var response = json(429, ["error": "locked_out", "retryAfter": seconds])
            response.headers.append(("Retry-After", String(seconds)))
            return response
        }
    }

    private func redirect(host: String, httpsPort: Int, uri: String) -> HTTPResponseInfo {
        var name = HostHeaderPolicy.hostname(from: host)
        if name.contains(":") { name = "[\(name)]" }
        let authority = httpsPort == 443 ? name : "\(name):\(httpsPort)"
        // Only keep a plain path, so the Location header cannot be abused.
        let path = uri.hasPrefix("/") && uri.unicodeScalars.allSatisfy({ $0.value > 0x20 && $0.value < 0x7F }) ? uri : "/"
        // 307 rather than a permanent redirect: browsers would cache that forever.
        return text(307, "Use https://\(authority)/", extra: [("Location", "https://\(authority)\(path)")])
    }

    private func json(_ status: Int, _ object: [String: Any]) -> HTTPResponseInfo {
        let data = (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data("{}".utf8)
        return HTTPResponseInfo(status: status, headers: [("Content-Type", "application/json")], body: data)
    }

    private func text(_ status: Int, _ body: String, extra: [(String, String)] = []) -> HTTPResponseInfo {
        HTTPResponseInfo(status: status, headers: [("Content-Type", "text/plain; charset=utf-8")] + extra, body: Data(body.utf8))
    }
}

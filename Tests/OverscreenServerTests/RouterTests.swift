import Foundation
import XCTest
import OverscreenCore
@testable import OverscreenServer

final class RouterTests: XCTestCase {
    var now = 0.0
    var context: ServerContext!
    var router: Router!

    override func setUp() {
        let pairing = PairingManager(clock: { [unowned self] in self.now })
        var settings = HostSettings()
        settings.displays = [DisplayConfig(id: 0, name: "Display <b>1</b>")]
        context = ServerContext(settings: settings, pairing: pairing, appVersion: "1.0", hostName: "Test Mac")
        router = Router(context: context)
    }

    func get(_ uri: String, host: String = "192.168.1.5", headers: [(String, String)] = []) -> HTTPResponseInfo {
        router.handle(HTTPRequestInfo(method: "GET", uri: uri, headers: [("Host", host)] + headers))
    }

    func post(_ uri: String, json: String, contentType: String = "application/json", from: String = "192.168.1.9") -> HTTPResponseInfo {
        router.handle(HTTPRequestInfo(method: "POST", uri: uri, headers: [("Host", "192.168.1.5"), ("Content-Type", contentType)],
                                      body: Data(json.utf8), remoteAddress: from))
    }

    func jsonBody(_ r: HTTPResponseInfo) -> [String: Any] {
        (try? JSONSerialization.jsonObject(with: r.body) as? [String: Any]) ?? [:]
    }

    func testServesClientWithSecurityHeaders() {
        let r = get("/")
        XCTAssertEqual(r.status, 200)
        XCTAssertEqual(r.header("Content-Type"), "text/html; charset=utf-8")
        XCTAssertNotNil(r.header("Content-Security-Policy"))
        XCTAssertEqual(r.header("X-Frame-Options"), "DENY")
        XCTAssertNil(r.header("Access-Control-Allow-Origin"), "no CORS")
        XCTAssertEqual(get("/client.js?v=3").status, 200)
        XCTAssertEqual(get("/gestures.js").status, 200)
    }

    func testNoFilesystemAccess() {
        for path in ["/../etc/passwd", "/C:/Windows/win.ini", "/%2e%2e/%2e%2e/etc/passwd", "//etc/passwd", "/Package.swift", "/WebClient/index.html"] {
            XCTAssertEqual(get(path).status, 404, path)
        }
    }

    func testRejectsForeignHost() {
        XCTAssertEqual(get("/", host: "attacker.example").status, 421)
        XCTAssertEqual(router.handle(HTTPRequestInfo(method: "GET", uri: "/")).status, 421, "missing Host")
    }

    func testInfo() {
        let r = get("/api/info")
        XCTAssertEqual(r.status, 200)
        let body = jsonBody(r)
        XCTAssertEqual(body["name"] as? String, "Test Mac")
        XCTAssertEqual(body["pairingRequired"] as? Bool, true)
        let displays = body["displays"] as? [[String: Any]]
        XCTAssertEqual(displays?.first?["name"] as? String, "Display <b>1</b>")
        XCTAssertEqual(displays?.first?["inUse"] as? Bool, false)
    }

    func testPairingFlow() {
        XCTAssertEqual(post("/api/pair", json: #"{"code":"1"}"#, contentType: "text/plain").status, 415)
        XCTAssertEqual(post("/api/pair", json: "nope").status, 400)
        let wrong = context.pairing.currentCode == "000000" ? "111111" : "000000"
        let bad = post("/api/pair", json: #"{"code":"\#(wrong)"}"#)
        XCTAssertEqual(bad.status, 401)
        XCTAssertEqual(jsonBody(bad)["remainingAttempts"] as? Int, 4)

        let ok = post("/api/pair", json: #"{"code":"\#(context.pairing.currentCode)"}"#)
        XCTAssertEqual(ok.status, 200)
        let token = jsonBody(ok)["token"] as? String ?? ""
        XCTAssertEqual(get("/api/auth", headers: [("Authorization", "Bearer \(token)")]).status, 204)
        XCTAssertEqual(get("/api/auth", headers: [("Authorization", "Bearer nope")]).status, 401)
        XCTAssertEqual(get("/api/auth").status, 401)
    }

    func testLockoutReturns429() {
        let wrong = context.pairing.currentCode == "000000" ? "111111" : "000000"
        for _ in 0..<4 { _ = post("/api/pair", json: #"{"code":"\#(wrong)"}"#) }
        let r = post("/api/pair", json: #"{"code":"\#(wrong)"}"#)
        XCTAssertEqual(r.status, 429)
        XCTAssertEqual(r.header("Retry-After"), "30")
    }

    func testOpenModeIssuesTokens() {
        context.settings.requirePairing = false
        let r = post("/api/pair", json: "{}")
        XCTAssertEqual(r.status, 200)
        XCTAssertTrue(context.pairing.isValid(token: jsonBody(r)["token"] as? String ?? ""))
    }

    func testWebSocketAuthorization() {
        let token = context.pairing.issueUnauthenticatedToken()
        func ws(_ uri: String, host: String = "192.168.1.5:8080", origin: String? = "http://192.168.1.5:8080") -> Bool {
            var headers = [("Host", host)]
            if let origin { headers.append(("Origin", origin)) }
            return router.authorizeWebSocket(HTTPRequestInfo(method: "GET", uri: uri, headers: headers))
        }
        XCTAssertTrue(ws("/ws?token=\(token)"))
        XCTAssertFalse(ws("/ws"))
        XCTAssertFalse(ws("/ws?token=deadbeef"))
        XCTAssertFalse(ws("/ws?token=\(token)", origin: "http://evil.example"))
        XCTAssertFalse(ws("/ws?token=\(token)", host: "evil.example", origin: "http://evil.example"))
        XCTAssertFalse(ws("/other?token=\(token)"))
        XCTAssertEqual(get("/ws").status, 401)
    }

    func testUnknownApi() {
        XCTAssertEqual(get("/api/nope").status, 404)
        XCTAssertEqual(post("/api/info", json: "{}").status, 405)
        XCTAssertEqual(router.handle(HTTPRequestInfo(method: "DELETE", uri: "/", headers: [("Host", "localhost")])).status, 405)
    }

    func testHeadHasNoBody() {
        let r = router.handle(HTTPRequestInfo(method: "HEAD", uri: "/", headers: [("Host", "localhost")]))
        XCTAssertEqual(r.status, 200)
        XCTAssertTrue(r.body.isEmpty)
    }

    func testEmbeddedAssetsMatchSources() throws {
        let webClient = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().appendingPathComponent("WebClient")
        let names = try FileManager.default.contentsOfDirectory(atPath: webClient.path).filter { !$0.hasPrefix(".") }
        XCTAssertEqual(Set(names), Set(WebAssets.files.keys), "run scripts/embed-webclient.py")
        for name in names {
            let source = try String(contentsOf: webClient.appendingPathComponent(name), encoding: .utf8)
            var trimmed = source
            while trimmed.hasSuffix("\n") { trimmed.removeLast() }
            XCTAssertEqual(WebAssets.files[name]?.body, trimmed, "\(name) is stale: run scripts/embed-webclient.py")
        }
    }
}

final class SessionHubTests: XCTestCase {
    func testNewSessionReplacesOldOnSameDisplay() {
        let hub = SessionHub()
        var kicked: [String] = []
        hub.register(ConnectedClient(id: "a", displayIndex: 0, remoteAddress: "x", connectedAt: Date())) { kicked.append("a:\($0)") }
        hub.register(ConnectedClient(id: "b", displayIndex: 1, remoteAddress: "x", connectedAt: Date())) { kicked.append("b:\($0)") }
        let total = hub.register(ConnectedClient(id: "c", displayIndex: 0, remoteAddress: "y", connectedAt: Date())) { kicked.append("c:\($0)") }
        XCTAssertEqual(kicked, ["a:replaced"])
        XCTAssertEqual(total, 2)
        XCTAssertEqual(hub.clients.map(\.id), ["c", "b"])
        hub.update("c", latencyMs: 7)
        XCTAssertEqual(hub.clients.first?.latencyMs, 7)
        hub.disconnectAll()
        XCTAssertEqual(Set(kicked), ["a:replaced", "b:host_stopped", "c:host_stopped"])
        XCTAssertEqual(hub.count, 0)
    }
}

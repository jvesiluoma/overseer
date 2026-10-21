import Foundation
import XCTest
import OverscreenCore
@testable import OverscreenServer

final class TLSIdentityTests: XCTestCase {
    var directory: URL!

    override func setUp() {
        directory = FileManager.default.temporaryDirectory.appendingPathComponent("overscreen-tls-\(UUID().uuidString)")
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: directory)
    }

    func load(now: Date = Date()) throws -> TLSIdentity {
        try TLSIdentity.loadOrCreate(directory: directory, commonName: "Test", dnsNames: ["localhost", "mac.local"],
                                     ipAddresses: ["192.168.1.5", "::1", "not-an-ip"], now: now)
    }

    func testCreatesOnceAndReuses() throws {
        let first = try load()
        let second = try load()
        XCTAssertEqual(first.fingerprint, second.fingerprint, "fingerprint must survive restarts")
        XCTAssertEqual(first.fingerprint.count, 32 * 3 - 1)
        XCTAssertNoThrow(try first.makeSSLContext())
    }

    func testKeyIsPrivate() throws {
        _ = try load()
        let attributes = try FileManager.default.attributesOfItem(atPath: directory.appendingPathComponent(TLSIdentity.keyFile).path)
        XCTAssertEqual((attributes[.posixPermissions] as? NSNumber)?.intValue, 0o600)
        let dir = try FileManager.default.attributesOfItem(atPath: directory.path)
        XCTAssertEqual((dir[.posixPermissions] as? NSNumber)?.intValue, 0o700)
    }

    func testValidityWithinApplesLimit() throws {
        let now = Date()
        let identity = try load(now: now)
        XCTAssertLessThanOrEqual(identity.notValidAfter.timeIntervalSince(now), 825 * 86_400)
    }

    func testRenewsBeforeExpiry() throws {
        let first = try load()
        let later = try load(now: first.notValidAfter.addingTimeInterval(-86_400))
        XCTAssertNotEqual(first.fingerprint, later.fingerprint)
    }

    func testReplacesCorruptFiles() throws {
        let first = try load()
        try Data("garbage".utf8).write(to: directory.appendingPathComponent(TLSIdentity.keyFile))
        XCTAssertNotEqual(try load().fingerprint, first.fingerprint)
    }

    func testRejectsMismatchedKey() throws {
        let a = try TLSIdentity.generate(commonName: "A", dnsNames: [], ipAddresses: [])
        let b = try TLSIdentity.generate(commonName: "B", dnsNames: [], ipAddresses: [])
        XCTAssertThrowsError(try TLSIdentity(certificatePEM: a.certificatePEM, privateKeyPEM: b.privateKeyPEM))
    }
}

final class RedirectTests: XCTestCase {
    var context: ServerContext!
    var router: Router!

    override func setUp() {
        context = ServerContext(settings: HostSettings(), appVersion: "1.0", hostName: "Test Mac")
        router = Router(context: context, transport: .redirectToHTTPS(port: 8443))
    }

    func request(_ method: String, _ uri: String, host: String = "192.168.1.5", body: String = "") -> HTTPResponseInfo {
        router.handle(HTTPRequestInfo(method: method, uri: uri, headers: [("Host", host), ("Content-Type", "application/json")],
                                      body: Data(body.utf8)))
    }

    func testRedirectsEverythingToHTTPS() {
        let r = request("GET", "/client.js?v=1", host: "192.168.1.5:80")
        XCTAssertEqual(r.status, 307)
        XCTAssertEqual(r.header("Location"), "https://192.168.1.5:8443/client.js?v=1")
    }

    func testDoesNotServeAPIUnencrypted() {
        let code = context.pairing.currentCode
        let r = request("POST", "/api/pair", body: #"{"code":"\#(code)"}"#)
        XCTAssertEqual(r.status, 307)
        XCTAssertFalse(String(decoding: r.body, as: UTF8.self).contains("token"))
        XCTAssertEqual(context.pairing.currentCode, code, "code was not consumed")
    }

    func testRefusesWebSocket() {
        let token = context.pairing.issueUnauthenticatedToken()
        XCTAssertFalse(router.authorizeWebSocket(HTTPRequestInfo(method: "GET", uri: "/ws?token=\(token)", headers: [("Host", "192.168.1.5")])))
    }

    func testDefaultPortAndIPv6() {
        let r443 = Router(context: context, transport: .redirectToHTTPS(port: 443))
        let r = r443.handle(HTTPRequestInfo(method: "GET", uri: "/", headers: [("Host", "[fe80::1]:80")]))
        XCTAssertEqual(r.header("Location"), "https://[fe80::1]/")
    }

    func testStillChecksHostAndSanitizesPath() {
        XCTAssertEqual(request("GET", "/", host: "evil.example").status, 421, "no open redirect for rebinding hosts")
        XCTAssertEqual(request("GET", "*").header("Location"), "https://192.168.1.5:8443/")
        XCTAssertEqual(request("GET", "/a b").header("Location"), "https://192.168.1.5:8443/")
    }
}

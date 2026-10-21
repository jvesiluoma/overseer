import Foundation
import XCTest
@testable import OverscreenCore

final class SignalingCodecTests: XCTestCase {
    func testDecodesHello() throws {
        let msg = try SignalingCodec.decodeClient(Data(#"{"type":"hello","display":1,"viewport":{"width":1280,"height":800,"dpr":2}}"#.utf8))
        XCTAssertEqual(msg, .hello(display: 1, viewport: Viewport(width: 1280, height: 800, dpr: 2)))
    }

    func testRejectsImplausibleViewport() {
        XCTAssertThrowsError(try SignalingCodec.decodeClient(Data(#"{"type":"hello","viewport":{"width":1e9,"height":800,"dpr":1}}"#.utf8)))
        XCTAssertThrowsError(try SignalingCodec.decodeClient(Data(#"{"type":"hello","display":-1,"viewport":{"width":800,"height":600,"dpr":1}}"#.utf8)))
    }

    func testRejectsUnknownAndMalformed() {
        XCTAssertThrowsError(try SignalingCodec.decodeClient(Data(#"{"type":"exec"}"#.utf8))) { error in
            XCTAssertEqual(error as? ProtocolError, .unknownType("exec"))
        }
        XCTAssertThrowsError(try SignalingCodec.decodeClient(Data("not json".utf8)))
        XCTAssertThrowsError(try SignalingCodec.decodeClient(Data(repeating: 0x20, count: maxSignalingMessageBytes + 1)))
    }

    func testDecodesCandidateAndAnswer() throws {
        let c = try SignalingCodec.decodeClient(Data(#"{"type":"candidate","candidate":"candidate:1 1 udp 1 1.2.3.4 5 typ host","sdpMid":"0","sdpMLineIndex":0}"#.utf8))
        XCTAssertEqual(c, .candidate(IceCandidate(candidate: "candidate:1 1 udp 1 1.2.3.4 5 typ host", sdpMid: "0", sdpMLineIndex: 0)))
        XCTAssertThrowsError(try SignalingCodec.decodeClient(Data(#"{"type":"answer","sdp":""}"#.utf8)))
    }

    func testEncodesCompactMessages() throws {
        let data = SignalingCodec.encode(.ping(id: 3, latencyMs: nil))
        XCTAssertEqual(String(decoding: data, as: UTF8.self), #"{"id":3,"type":"ping"}"#)
        let status = SignalingCodec.encode(.status(state: .streaming, width: 1920, height: 1200))
        XCTAssertEqual(String(decoding: status, as: UTF8.self), #"{"height":1200,"state":"streaming","type":"status","width":1920}"#)
    }
}

final class InputTests: XCTestCase {
    var now = 0.0
    lazy var mapper = InputMapper(bounds: DisplayRect(x: 1000, y: 0, width: 1000, height: 500), clock: { [unowned self] in self.now })

    func testDecodesEvents() throws {
        XCTAssertEqual(try InputCodec.decode(Data(#"{"t":"down","x":0.5,"y":0.25}"#.utf8)), .down(x: 0.5, y: 0.25))
        XCTAssertEqual(try InputCodec.decode(Data(#"{"t":"scroll","x":0.5,"y":0.5,"dy":-0.1}"#.utf8)), .scroll(x: 0.5, y: 0.5, dx: 0, dy: -0.1))
        XCTAssertEqual(try InputCodec.decode(Data(#"{"t":"stats","rtt":12.5,"fps":30}"#.utf8)), .stats(ClientStats(rttMs: 12.5, fps: 30)))
        XCTAssertThrowsError(try InputCodec.decode(Data(#"{"t":"down","x":0.5}"#.utf8)))
        XCTAssertThrowsError(try InputCodec.decode(Data(#"{"t":"key","code":"KeyA"}"#.utf8)))
    }

    func testMapsIntoDisplayAndClamps() {
        XCTAssertEqual(mapper.point(x: 0.5, y: 0.5), Point(x: 1500, y: 250))
        XCTAssertEqual(mapper.point(x: 7, y: -3), Point(x: 1999, y: 0))
        XCTAssertEqual(mapper.point(x: -1, y: 1), Point(x: 1000, y: 499))
    }

    func testTapThenDoubleTap() {
        XCTAssertEqual(mapper.handle(.down(x: 0.5, y: 0.5)), [.move(Point(x: 1500, y: 250)), .leftDown(Point(x: 1500, y: 250), clickCount: 1)])
        XCTAssertEqual(mapper.handle(.up(x: 0.5, y: 0.5)), [.leftUp(Point(x: 1500, y: 250), clickCount: 1)])
        now = 0.2
        XCTAssertEqual(mapper.handle(.down(x: 0.505, y: 0.5)).last, .leftDown(Point(x: 1505, y: 250), clickCount: 2))
        XCTAssertEqual(mapper.handle(.up(x: 0.505, y: 0.5)), [.leftUp(Point(x: 1505, y: 250), clickCount: 2)])
        now = 0.4
        XCTAssertEqual(mapper.handle(.down(x: 0.5, y: 0.5)).last, .leftDown(Point(x: 1500, y: 250), clickCount: 3))
    }

    func testSlowOrDistantTapsAreSingleClicks() {
        _ = mapper.handle(.down(x: 0.5, y: 0.5)); _ = mapper.handle(.up(x: 0.5, y: 0.5))
        now = 1.0
        XCTAssertEqual(mapper.handle(.down(x: 0.5, y: 0.5)).last, .leftDown(Point(x: 1500, y: 250), clickCount: 1))
        _ = mapper.handle(.up(x: 0.5, y: 0.5))
        now = 1.1
        XCTAssertEqual(mapper.handle(.down(x: 0.9, y: 0.5)).last, .leftDown(Point(x: 1900, y: 250), clickCount: 1))
    }

    func testDragAndHover() {
        XCTAssertEqual(mapper.handle(.move(x: 0.1, y: 0.1)), [.move(Point(x: 1100, y: 50))])
        XCTAssertEqual(mapper.handle(.move(x: 0.1, y: 0.1)), [], "duplicate positions are dropped")
        _ = mapper.handle(.down(x: 0.1, y: 0.1))
        XCTAssertEqual(mapper.handle(.move(x: 0.2, y: 0.1)), [.drag(Point(x: 1200, y: 50))])
        XCTAssertEqual(mapper.reset(), [.leftUp(Point(x: 1200, y: 50), clickCount: 1)])
        XCTAssertEqual(mapper.reset(), [])
    }

    func testScrollReleasesButtonAndScales() {
        _ = mapper.handle(.down(x: 0.5, y: 0.5))
        let actions = mapper.handle(.scroll(x: 0.5, y: 0.5, dx: 0.01, dy: -0.1))
        XCTAssertEqual(actions, [.leftUp(Point(x: 1500, y: 250), clickCount: 1), .scroll(dx: 10, dy: -50)])
        mapper.invertScroll = true
        XCTAssertEqual(mapper.handle(.scroll(x: 0.5, y: 0.5, dx: 0, dy: -0.1)), [.scroll(dx: 0, dy: 50)])
    }

    func testRightClick() {
        XCTAssertEqual(mapper.handle(.rightClick(x: 0, y: 0)), [.rightClick(Point(x: 1000, y: 0))])
    }
}

final class PairingTests: XCTestCase {
    var now = 0.0

    func makeManager(_ configure: (inout PairingManager.Configuration) -> Void = { _ in }) -> PairingManager {
        var config = PairingManager.Configuration()
        configure(&config)
        return PairingManager(configuration: config, clock: { [unowned self] in self.now })
    }

    func testCodeShape() {
        let code = makeManager().currentCode
        XCTAssertEqual(code.count, 6)
        XCTAssertTrue(code.allSatisfy(\.isNumber))
    }

    func testSuccessIssuesTokenAndRotatesCode() {
        let m = makeManager()
        let code = m.currentCode
        guard case .success(let token) = m.attempt(code: code, client: "a") else { return XCTFail() }
        XCTAssertEqual(token.count, 32)
        XCTAssertTrue(m.isValid(token: token))
        XCTAssertFalse(m.isValid(token: String(token.reversed())))
        XCTAssertNotEqual(m.attempt(code: code, client: "b"), .success(token: token))
        m.revokeAll()
        XCTAssertFalse(m.isValid(token: token))
    }

    func testLockoutEscalates() {
        let m = makeManager()
        let wrong = m.currentCode == "000000" ? "111111" : "000000"
        for i in 1...4 { XCTAssertEqual(m.attempt(code: wrong, client: "a"), .invalid(remainingAttempts: 5 - i)) }
        XCTAssertEqual(m.attempt(code: wrong, client: "a"), .lockedOut(retryAfter: 30))
        // Even the right code is refused while locked out.
        XCTAssertEqual(m.attempt(code: m.currentCode, client: "a"), .lockedOut(retryAfter: 30))
        // Other clients are unaffected.
        if case .success = m.attempt(code: m.currentCode, client: "b") {} else { XCTFail() }
        now = 31
        let wrong2 = m.currentCode == "000000" ? "111111" : "000000"
        for _ in 1...4 { _ = m.attempt(code: wrong2, client: "a") }
        XCTAssertEqual(m.attempt(code: wrong2, client: "a"), .lockedOut(retryAfter: 60))
    }

    func testGlobalBudgetRotatesCode() {
        let m = makeManager { $0.globalFailureBudget = 3; $0.maxFailuresPerClient = 100 }
        let original = m.currentCode
        var rotations = 0
        m.onCodeChange = { _ in rotations += 1 }
        let wrong = original == "000000" ? "111111" : "000000"
        for i in 0..<3 { _ = m.attempt(code: wrong, client: "c\(i)") }
        XCTAssertEqual(rotations, 1)
    }

    func testTokenCap() {
        let m = makeManager { $0.maxTokens = 2 }
        let t1 = m.issueUnauthenticatedToken(), t2 = m.issueUnauthenticatedToken(), t3 = m.issueUnauthenticatedToken()
        XCTAssertFalse(m.isValid(token: t1))
        XCTAssertTrue(m.isValid(token: t2))
        XCTAssertTrue(m.isValid(token: t3))
    }

    func testConstantTimeEquals() {
        XCTAssertTrue(PairingManager.constantTimeEquals("123456", "123456"))
        XCTAssertFalse(PairingManager.constantTimeEquals("123456", "123457"))
        XCTAssertFalse(PairingManager.constantTimeEquals("123456", "1234567"))
        XCTAssertFalse(PairingManager.constantTimeEquals("", "1"))
    }
}

final class StreamingTests: XCTestCase {
    func testAutoModeUsesDevicePixels() {
        let mode = DisplayModeCalculator.mode(for: Viewport(width: 1180, height: 820, dpr: 2), config: DisplayConfig(id: 0, name: "iPad"))
        XCTAssertEqual(mode, DisplayMode(pixelWidth: 2360, pixelHeight: 1640, hiDPI: false))
    }

    func testHiDPIHalvesPoints() {
        let mode = DisplayModeCalculator.mode(for: Viewport(width: 1181, height: 821, dpr: 2), config: DisplayConfig(id: 0, name: "x", hiDPI: true))
        XCTAssertEqual(mode.pixelWidth % 4, 0)
        XCTAssertEqual(mode.pointWidth * 2, mode.pixelWidth)
        XCTAssertEqual(mode.pointHeight * 2, mode.pixelHeight)
    }

    func testClampsKeepingAspect() {
        let mode = DisplayModeCalculator.mode(for: Viewport(width: 3840, height: 2160, dpr: 2), config: DisplayConfig(id: 0, name: "x"))
        XCTAssertEqual(mode.pixelWidth, 3840)
        XCTAssertEqual(mode.pixelHeight, 2160)
        let tiny = DisplayModeCalculator.mode(for: Viewport(width: 320, height: 240, dpr: 1), config: DisplayConfig(id: 0, name: "x"))
        XCTAssertEqual(tiny, DisplayMode(pixelWidth: 640, pixelHeight: 480, hiDPI: false))
    }

    func testCustomAndFallback() {
        let custom = DisplayModeCalculator.mode(for: nil, config: DisplayConfig(id: 0, name: "x", resolution: .custom(width: 1921, height: 1081)))
        XCTAssertEqual(custom, DisplayMode(pixelWidth: 1920, pixelHeight: 1080, hiDPI: false))
        XCTAssertEqual(DisplayModeCalculator.mode(for: nil, config: DisplayConfig(id: 0, name: "x")).pixelWidth, 1920)
    }

    func testBitrate() {
        let mode = DisplayMode(pixelWidth: 1920, pixelHeight: 1200, hiDPI: false)
        XCTAssertEqual(BitratePolicy.bitrate(for: mode, fps: 30, maxMbps: 8, sessions: 1), 6_912_000)
        XCTAssertEqual(BitratePolicy.bitrate(for: mode, fps: 30, maxMbps: 8, sessions: 2), 4_000_000)
        XCTAssertEqual(BitratePolicy.bitrate(for: mode, fps: 30, maxMbps: 2, sessions: 4), BitratePolicy.floorBps)
    }

    func testSettingsSanitized() {
        var s = HostSettings()
        s.port = 0
        s.fps = 17
        s.maxBitrateMbps = 900
        s.displays = []
        let clean = s.sanitized()
        XCTAssertEqual(clean.port, 80)
        XCTAssertEqual(clean.fps, 30)
        XCTAssertEqual(clean.maxBitrateMbps, 50)
        XCTAssertEqual(clean.displays.count, 1)
    }

    func testSettingsFromOlderVersionKeepValues() throws {
        let old = #"{"port":8080,"fallbackPort":8081,"requirePairing":false,"allowInput":true,"invertScroll":true,"fps":60,"#
            + #""maxBitrateMbps":12,"displays":[{"id":0,"name":"Car","resolution":{"auto":{}},"hiDPI":true}],"extraHostNames":[]}"#
        let s = try JSONDecoder().decode(HostSettings.self, from: Data(old.utf8))
        XCTAssertEqual(s.port, 8080)
        XCTAssertEqual(s.fps, 60)
        XCTAssertEqual(s.displays.first?.name, "Car")
        XCTAssertFalse(s.requirePairing)
        XCTAssertEqual(s.httpsPort, 443, "new keys get defaults")
        XCTAssertFalse(s.allowInsecureHTTP, "insecure HTTP stays off for upgraded settings")
    }

    func testSettingsRoundTrip() throws {
        var s = HostSettings()
        s.displays = [DisplayConfig(id: 0, name: "Car", resolution: .custom(width: 1920, height: 1200), hiDPI: true)]
        let decoded = try JSONDecoder().decode(HostSettings.self, from: JSONEncoder().encode(s))
        XCTAssertEqual(decoded, s)
    }
}

final class NetworkTests: XCTestCase {
    func testHostHeaderPolicy() {
        let extra = ["go.ss"]
        for ok in ["192.168.1.20", "192.168.1.20:8080", "localhost:80", "macbook.local", "GO.SS", "[fe80::1]:80", "203.0.113.1"] {
            XCTAssertTrue(HostHeaderPolicy.isAllowed(host: ok, extraNames: extra), ok)
        }
        for bad in ["evil.example", "192.168.1.20.evil.example", "", "1.2.3", "999.1.1.1", "local"] {
            XCTAssertFalse(HostHeaderPolicy.isAllowed(host: bad, extraNames: extra), bad)
        }
    }

    func testOriginMatching() {
        XCTAssertTrue(HostHeaderPolicy.originMatchesHost(origin: "http://192.168.1.2:8080", host: "192.168.1.2:8080"))
        XCTAssertTrue(HostHeaderPolicy.originMatchesHost(origin: "http://go.ss", host: "go.ss"))
        XCTAssertTrue(HostHeaderPolicy.originMatchesHost(origin: "http://go.ss", host: "go.ss:80"))
        XCTAssertTrue(HostHeaderPolicy.originMatchesHost(origin: nil, host: "go.ss"), "non-browser clients send no Origin")
        XCTAssertFalse(HostHeaderPolicy.originMatchesHost(origin: "http://evil.example", host: "192.168.1.2"))
        XCTAssertFalse(HostHeaderPolicy.originMatchesHost(origin: "null", host: "192.168.1.2"))
        XCTAssertFalse(HostHeaderPolicy.originMatchesHost(origin: "http://192.168.1.2:9999", host: "192.168.1.2:8080"))
        XCTAssertTrue(HostHeaderPolicy.originMatchesHost(origin: "https://192.168.1.2", host: "192.168.1.2:443"))
        XCTAssertTrue(HostHeaderPolicy.originMatchesHost(origin: "https://192.168.1.2:8443", host: "192.168.1.2:8443"))
        XCTAssertFalse(HostHeaderPolicy.originMatchesHost(origin: "https://192.168.1.2", host: "192.168.1.2:80"))
    }

    func testDetectsInternetSharing() {
        let addresses = [InterfaceAddress(interface: "en0", address: "192.168.1.106"),
                         InterfaceAddress(interface: "bridge0", address: "169.254.10.1"),
                         InterfaceAddress(interface: "bridge100", address: "192.168.2.1")]
        let hotspot = NetworkInterfaces.hotspot(in: addresses)
        XCTAssertEqual(hotspot?.kind, .internetSharing)
        XCTAssertEqual(hotspot?.address.address, "192.168.2.1")
        XCTAssertEqual(NetworkInterfaces.sorted(addresses).map(\.interface), ["bridge100", "en0", "bridge0"],
                       "the hotspot address comes first, so the QR code points at it")
    }

    func testDetectsIPhoneHotspot() {
        let addresses = [InterfaceAddress(interface: "utun4", address: "10.8.0.2"),
                         InterfaceAddress(interface: "en0", address: "172.20.10.3")]
        XCTAssertEqual(NetworkInterfaces.hotspot(in: addresses)?.kind, .iPhoneHotspot)
        XCTAssertEqual(NetworkInterfaces.sorted(addresses).first?.interface, "en0")
        XCTAssertFalse(NetworkInterfaces.isIPhoneHotspotAddress("172.20.10.1"), "that is the phone itself")
        XCTAssertFalse(NetworkInterfaces.isIPhoneHotspotAddress("172.20.11.3"))
    }

    func testInternetSharingWinsOverPhoneHotspot() {
        let addresses = [InterfaceAddress(interface: "en0", address: "172.20.10.3"),
                         InterfaceAddress(interface: "bridge101", address: "192.168.2.1")]
        XCTAssertEqual(NetworkInterfaces.hotspot(in: addresses)?.kind, .internetSharing)
    }

    func testOrdinaryNetworkIsNoHotspot() {
        let addresses = [InterfaceAddress(interface: "en0", address: "192.168.1.106"),
                         InterfaceAddress(interface: "bridge0", address: "10.0.0.5")]
        XCTAssertNil(NetworkInterfaces.hotspot(in: addresses))
        XCTAssertEqual(NetworkInterfaces.sorted(addresses).map(\.interface), ["en0", "bridge0"], "order is kept within a rank")
    }

    func testPrivateRanges() {
        XCTAssertTrue(NetworkInterfaces.isPrivateIPv4("10.0.0.1"))
        XCTAssertTrue(NetworkInterfaces.isPrivateIPv4("172.20.10.2"))
        XCTAssertFalse(NetworkInterfaces.isPrivateIPv4("172.32.0.1"))
        XCTAssertFalse(NetworkInterfaces.isPrivateIPv4("203.0.113.1"))
    }

    func testEnumeratesWithoutLoopback() {
        XCTAssertFalse(NetworkInterfaces.ipv4Addresses().contains { $0.address == "127.0.0.1" })
    }
}

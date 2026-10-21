import Foundation

/// Version of the browser <-> host protocol described in docs/PROTOCOL.md.
public let overscreenProtocolVersion = 1

/// Browser viewport in CSS pixels plus device pixel ratio.
public struct Viewport: Codable, Equatable, Sendable {
    public var width: Double
    public var height: Double
    public var dpr: Double

    public init(width: Double, height: Double, dpr: Double = 1) {
        self.width = width
        self.height = height
        self.dpr = dpr
    }

    /// Rejects values a real browser cannot produce (NaN, negative, absurdly large).
    public var isPlausible: Bool {
        width.isFinite && height.isFinite && dpr.isFinite
            && (64...16384).contains(width) && (64...16384).contains(height)
            && (0.5...4).contains(dpr)
    }
}

public struct IceCandidate: Codable, Equatable, Sendable {
    public var candidate: String
    public var sdpMid: String?
    public var sdpMLineIndex: Int32

    public init(candidate: String, sdpMid: String?, sdpMLineIndex: Int32) {
        self.candidate = candidate
        self.sdpMid = sdpMid
        self.sdpMLineIndex = sdpMLineIndex
    }
}

/// Messages the browser sends over the signaling WebSocket.
public enum ClientMessage: Equatable, Sendable {
    case hello(display: Int, viewport: Viewport)
    case answer(sdp: String)
    case candidate(IceCandidate)
    case viewport(Viewport)
    case pong(id: Int)
    case bye
}

public enum SessionState: String, Codable, Sendable {
    case preparing, streaming, stopped
}

/// Messages the host sends over the signaling WebSocket.
public enum ServerMessage: Equatable, Sendable {
    case offer(sdp: String)
    case candidate(IceCandidate)
    case ping(id: Int, latencyMs: Int?)
    case status(state: SessionState, width: Int?, height: Int?)
    case error(code: String, message: String)
    case bye(reason: String)
}

public enum ProtocolError: Error, Equatable {
    case malformed
    case unknownType(String)
    case missingField(String)
    case invalidValue(String)
}

/// Flat wire representation; every message is a JSON object with a "type" discriminator.
private struct WireMessage: Codable {
    var type: String
    var display: Int?
    var viewport: Viewport?
    var sdp: String?
    var candidate: String?
    var sdpMid: String?
    var sdpMLineIndex: Int32?
    var id: Int?
    var latencyMs: Int?
    var state: SessionState?
    var width: Int?
    var height: Int?
    var code: String?
    var message: String?
    var reason: String?

    init(type: String) { self.type = type }
}

/// Upper bound for a single signaling message; SDP offers are typically < 8 KiB.
public let maxSignalingMessageBytes = 64 * 1024

public enum SignalingCodec {
    public static func decodeClient(_ data: Data) throws -> ClientMessage {
        guard data.count <= maxSignalingMessageBytes else { throw ProtocolError.invalidValue("size") }
        let wire: WireMessage
        do {
            wire = try JSONDecoder().decode(WireMessage.self, from: data)
        } catch {
            throw ProtocolError.malformed
        }
        switch wire.type {
        case "hello":
            guard let viewport = wire.viewport else { throw ProtocolError.missingField("viewport") }
            guard viewport.isPlausible else { throw ProtocolError.invalidValue("viewport") }
            let display = wire.display ?? 0
            guard (0..<64).contains(display) else { throw ProtocolError.invalidValue("display") }
            return .hello(display: display, viewport: viewport)
        case "answer":
            guard let sdp = wire.sdp, !sdp.isEmpty else { throw ProtocolError.missingField("sdp") }
            return .answer(sdp: sdp)
        case "candidate":
            guard let candidate = wire.candidate else { throw ProtocolError.missingField("candidate") }
            return .candidate(IceCandidate(candidate: candidate, sdpMid: wire.sdpMid, sdpMLineIndex: wire.sdpMLineIndex ?? 0))
        case "viewport":
            guard let viewport = wire.viewport else { throw ProtocolError.missingField("viewport") }
            guard viewport.isPlausible else { throw ProtocolError.invalidValue("viewport") }
            return .viewport(viewport)
        case "pong":
            guard let id = wire.id else { throw ProtocolError.missingField("id") }
            return .pong(id: id)
        case "bye":
            return .bye
        default:
            throw ProtocolError.unknownType(wire.type)
        }
    }

    public static func encode(_ message: ServerMessage) -> Data {
        var wire: WireMessage
        switch message {
        case .offer(let sdp):
            wire = WireMessage(type: "offer")
            wire.sdp = sdp
        case .candidate(let c):
            wire = WireMessage(type: "candidate")
            wire.candidate = c.candidate
            wire.sdpMid = c.sdpMid
            wire.sdpMLineIndex = c.sdpMLineIndex
        case .ping(let id, let latency):
            wire = WireMessage(type: "ping")
            wire.id = id
            wire.latencyMs = latency
        case .status(let state, let width, let height):
            wire = WireMessage(type: "status")
            wire.state = state
            wire.width = width
            wire.height = height
        case .error(let code, let message):
            wire = WireMessage(type: "error")
            wire.code = code
            wire.message = message
        case .bye(let reason):
            wire = WireMessage(type: "bye")
            wire.reason = reason
        }
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        // Encoding a struct of strings/ints cannot fail.
        return (try? encoder.encode(wire)) ?? Data("{\"type\":\"error\"}".utf8)
    }
}

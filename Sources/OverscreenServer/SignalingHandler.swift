import Foundation
import NIOCore
import NIOWebSocket
import OverscreenCore

/// Sends signaling messages to one WebSocket from any thread.
final class SignalingOutlet: MediaSessionDelegate, @unchecked Sendable {
    let channel: Channel
    let hub: SessionHub

    init(channel: Channel, hub: SessionHub) {
        self.channel = channel
        self.hub = hub
    }

    func mediaSession(_ id: String, send message: ServerMessage) {
        send(message)
    }

    func mediaSession(_ id: String, didReceiveStats stats: ClientStats) {
        hub.update(id, stats: stats)
    }

    func mediaSessionDidEnd(_ id: String, reason: String) {
        send(.bye(reason: reason))
        close()
    }

    func send(_ message: ServerMessage) {
        let text = String(decoding: SignalingCodec.encode(message), as: UTF8.self)
        let channel = self.channel
        channel.eventLoop.execute {
            var buffer = channel.allocator.buffer(capacity: text.utf8.count)
            buffer.writeString(text)
            channel.writeAndFlush(WebSocketFrame(fin: true, opcode: .text, data: buffer), promise: nil)
        }
    }

    func close() {
        let channel = self.channel
        channel.eventLoop.execute {
            var buffer = channel.allocator.buffer(capacity: 2)
            buffer.write(webSocketErrorCode: .normalClosure)
            channel.writeAndFlush(WebSocketFrame(fin: true, opcode: .connectionClose, data: buffer)).whenComplete { _ in
                channel.close(promise: nil)
            }
        }
    }
}

/// One signaling WebSocket: hello -> media session, relays answer/candidates, keeps a ping clock.
final class SignalingHandler: ChannelInboundHandler, RemovableChannelHandler {
    typealias InboundIn = WebSocketFrame
    typealias OutboundOut = WebSocketFrame

    static let pingInterval: TimeAmount = .seconds(2)
    static let pongTimeout: Double = 10
    static let maxProtocolErrors = 5

    private let context: ServerContext
    private let engine: MediaEngine
    private let remoteAddress: String
    private var outlet: SignalingOutlet?
    private var session: MediaSession?
    private var sessionID: String?
    private var pingTask: RepeatedTask?
    private var pingID = 0
    private var pingSentAt: [Int: Double] = [:]
    private var lastPong = ProcessInfo.processInfo.systemUptime
    private var latencyMs: Int?
    private var protocolErrors = 0
    private var closed = false

    init(context: ServerContext, engine: MediaEngine, remoteAddress: String) {
        self.context = context
        self.engine = engine
        self.remoteAddress = remoteAddress
    }

    func handlerAdded(context: ChannelHandlerContext) {
        outlet = SignalingOutlet(channel: context.channel, hub: self.context.hub)
        lastPong = ProcessInfo.processInfo.systemUptime
        pingTask = context.eventLoop.scheduleRepeatedTask(initialDelay: Self.pingInterval, delay: Self.pingInterval) { [weak self] _ in
            self?.sendPing()
        }
    }

    func handlerRemoved(context: ChannelHandlerContext) {
        teardown()
    }

    func channelInactive(context: ChannelHandlerContext) {
        teardown()
        context.fireChannelInactive()
    }

    func channelRead(context: ChannelHandlerContext, data: NIOAny) {
        let frame = unwrapInboundIn(data)
        switch frame.opcode {
        case .text:
            var data = frame.unmaskedData
            let bytes = data.readBytes(length: data.readableBytes) ?? []
            handleText(Data(bytes))
        case .ping:
            var payload = frame.unmaskedData
            let echo = context.channel.allocator.buffer(bytes: payload.readBytes(length: payload.readableBytes) ?? [])
            context.writeAndFlush(wrapOutboundOut(WebSocketFrame(fin: true, opcode: .pong, data: echo)), promise: nil)
        case .connectionClose:
            outlet?.close()
        default:
            break
        }
    }

    func errorCaught(context: ChannelHandlerContext, error: Error) {
        context.close(promise: nil)
    }

    private func handleText(_ data: Data) {
        let message: ClientMessage
        do {
            message = try SignalingCodec.decodeClient(data)
        } catch {
            protocolErrors += 1
            outlet?.send(.error(code: "bad_message", message: "\(error)"))
            if protocolErrors >= Self.maxProtocolErrors { outlet?.close() }
            return
        }
        switch message {
        case .hello(let display, let viewport):
            startSession(display: display, viewport: viewport)
        case .answer, .candidate, .viewport:
            session?.handle(message)
        case .pong(let id):
            lastPong = ProcessInfo.processInfo.systemUptime
            if let sent = pingSentAt.removeValue(forKey: id), let sessionID {
                let ms = Int(((lastPong - sent) * 1000).rounded())
                latencyMs = ms
                context.hub.update(sessionID, latencyMs: ms)
            }
        case .bye:
            outlet?.close()
        }
    }

    private func startSession(display: Int, viewport: Viewport) {
        guard session == nil, let outlet else {
            outlet?.send(.error(code: "already_started", message: "hello was already sent"))
            return
        }
        let settings = context.settings
        guard let config = settings.displays.first(where: { $0.id == display }) else {
            outlet.send(.error(code: "no_such_display", message: "display \(display) is not configured"))
            return
        }
        let id = SignalingHandler.makeSessionID()
        let client = ConnectedClient(id: id, displayIndex: display, remoteAddress: remoteAddress, connectedAt: Date())
        let total = context.hub.register(client) { [weak outlet] reason in
            outlet?.send(.bye(reason: reason))
            outlet?.close()
        }
        let request = MediaSessionRequest(id: id, display: config, viewport: viewport, settings: settings, concurrentSessions: total)
        do {
            session = try engine.makeSession(request, delegate: outlet)
            sessionID = id
        } catch {
            context.hub.unregister(id)
            outlet.send(.error(code: "start_failed", message: "\(error)"))
            outlet.close()
        }
    }

    private func sendPing() {
        let now = ProcessInfo.processInfo.systemUptime
        if now - lastPong > Self.pongTimeout {
            outlet?.send(.bye(reason: "timeout"))
            outlet?.close()
            return
        }
        pingID += 1
        pingSentAt[pingID] = now
        pingSentAt = pingSentAt.filter { now - $0.value < Self.pongTimeout }
        outlet?.send(.ping(id: pingID, latencyMs: latencyMs))
    }

    private func teardown() {
        guard !closed else { return }
        closed = true
        pingTask?.cancel()
        pingTask = nil
        session?.close()
        session = nil
        if let sessionID { context.hub.unregister(sessionID) }
    }

    static func makeSessionID() -> String {
        var rng = SystemRandomNumberGenerator()
        return (0..<8).map { _ in String(format: "%02x", UInt8.random(in: 0...255, using: &rng)) }.joined()
    }
}

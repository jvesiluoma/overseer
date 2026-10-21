import Foundation
import NIOCore
import NIOHTTP1
import NIOPosix
import NIOSSL
import NIOWebSocket
import OverscreenCore

/// HTTP + WebSocket server serving the web client, the pairing API and signaling.
public final class OverscreenHTTPServer: @unchecked Sendable {
    public let context: ServerContext
    private let router: Router
    private let engine: MediaEngine
    private let sslContext: NIOSSLContext?
    private let group: EventLoopGroup
    private let ownsGroup: Bool
    private var channel: Channel?

    public init(context: ServerContext, engine: MediaEngine, transport: ServerTransport = .plain,
                group: EventLoopGroup? = nil) throws {
        self.context = context
        self.router = Router(context: context, transport: transport)
        self.engine = engine
        if case .tls(let identity) = transport {
            sslContext = try identity.makeSSLContext()
        } else {
            sslContext = nil
        }
        if let group {
            self.group = group
            self.ownsGroup = false
        } else {
            self.group = MultiThreadedEventLoopGroup(numberOfThreads: 2)
            self.ownsGroup = true
        }
    }

    deinit {
        if ownsGroup { try? group.syncShutdownGracefully() }
    }

    /// Binds the first port that works and returns it. Port 0 picks an ephemeral port.
    @discardableResult
    public func start(host: String = "0.0.0.0", ports: [Int]) throws -> Int {
        var lastError: Error = ServerError.noPorts
        for port in ports {
            do {
                let channel = try makeBootstrap().bind(host: host, port: port).wait()
                self.channel = channel
                return channel.localAddress?.port ?? port
            } catch {
                lastError = error
            }
        }
        throw lastError
    }

    public func stop() {
        context.hub.disconnectAll()
        try? channel?.close().wait()
        channel = nil
    }

    public var boundPort: Int? { channel?.localAddress?.port }

    private func makeBootstrap() -> ServerBootstrap {
        let router = self.router
        let context = self.context
        let engine = self.engine
        let sslContext = self.sslContext
        return ServerBootstrap(group: group)
            .serverChannelOption(ChannelOptions.backlog, value: 64)
            .serverChannelOption(ChannelOptions.socketOption(.so_reuseaddr), value: 1)
            .childChannelOption(ChannelOptions.tcpOption(.tcp_nodelay), value: 1)
            .childChannelInitializer { channel in
                let httpHandler = HTTPHandler(router: router)
                let upgrader = NIOWebSocketServerUpgrader(
                    maxFrameSize: maxSignalingMessageBytes,
                    automaticErrorHandling: true,
                    shouldUpgrade: { channel, head in
                        let info = HTTPHandler.requestInfo(head: head, body: Data(), channel: channel)
                        return channel.eventLoop.makeSucceededFuture(router.authorizeWebSocket(info) ? HTTPHeaders() : nil)
                    },
                    upgradePipelineHandler: { channel, _ in
                        let remote = channel.remoteAddress?.ipAddress ?? "unknown"
                        let handler = SignalingHandler(context: context, engine: engine, remoteAddress: remote)
                        return channel.eventLoop.makeCompletedFuture {
                            try channel.pipeline.syncOperations.addHandlers([
                                NIOWebSocketFrameAggregator(minNonFinalFragmentSize: 0, maxAccumulatedFrameCount: 64,
                                                            maxAccumulatedFrameSize: maxSignalingMessageBytes),
                                handler,
                            ])
                        }
                    }
                )
                let config: NIOHTTPServerUpgradeConfiguration = (
                    upgraders: [upgrader],
                    completionHandler: { context in
                        context.pipeline.syncOperations.removeHandler(name: "overscreen.http", promise: nil)
                    }
                )
                return channel.eventLoop.makeCompletedFuture {
                    if let sslContext {
                        try channel.pipeline.syncOperations.addHandler(NIOSSLServerHandler(context: sslContext))
                    }
                    try channel.pipeline.syncOperations.configureHTTPServerPipeline(withServerUpgrade: config)
                    try channel.pipeline.syncOperations.addHandler(httpHandler, name: "overscreen.http")
                }
            }
    }
}

public enum ServerError: Error {
    case noPorts
}

final class HTTPHandler: ChannelInboundHandler, RemovableChannelHandler {
    typealias InboundIn = HTTPServerRequestPart
    typealias OutboundOut = HTTPServerResponsePart

    private let router: Router
    private var head: HTTPRequestHead?
    private var body = Data()
    private var tooLarge = false

    init(router: Router) {
        self.router = router
    }

    static func requestInfo(head: HTTPRequestHead, body: Data, channel: Channel) -> HTTPRequestInfo {
        HTTPRequestInfo(method: head.method.rawValue, uri: head.uri, headers: head.headers.map { ($0.name, $0.value) },
                        body: body, remoteAddress: channel.remoteAddress?.ipAddress ?? "unknown")
    }

    func channelRead(context: ChannelHandlerContext, data: NIOAny) {
        switch unwrapInboundIn(data) {
        case .head(let h):
            head = h
            body = Data()
            tooLarge = false
        case .body(let buffer):
            if body.count + buffer.readableBytes > Router.maxBodyBytes {
                tooLarge = true
            } else {
                body.append(contentsOf: buffer.readableBytesView)
            }
        case .end:
            guard let head else { return }
            self.head = nil
            let response: HTTPResponseInfo
            if tooLarge {
                response = HTTPResponseInfo(status: 413, headers: [("Content-Type", "text/plain")], body: Data("Too large".utf8))
            } else {
                response = router.handle(HTTPHandler.requestInfo(head: head, body: body, channel: context.channel))
            }
            write(response, for: head, context: context)
        }
    }

    private func write(_ response: HTTPResponseInfo, for request: HTTPRequestHead, context: ChannelHandlerContext) {
        // NIO only honours a WebSocket upgrade on a connection's first request, and clients reuse
        // pooled connections for the upgrade. The client makes a handful of requests, so always close.
        var headers = HTTPHeaders(response.headers)
        headers.replaceOrAdd(name: "Content-Length", value: String(response.body.count))
        headers.replaceOrAdd(name: "Connection", value: "close")
        let responseHead = HTTPResponseHead(version: request.version, status: HTTPResponseStatus(statusCode: response.status),
                                            headers: headers)
        context.write(wrapOutboundOut(.head(responseHead)), promise: nil)
        if !response.body.isEmpty {
            var buffer = context.channel.allocator.buffer(capacity: response.body.count)
            buffer.writeBytes(response.body)
            context.write(wrapOutboundOut(.body(.byteBuffer(buffer))), promise: nil)
        }
        let channel = context.channel
        context.writeAndFlush(wrapOutboundOut(.end(nil))).whenComplete { _ in
            channel.close(promise: nil)
        }
    }

    func errorCaught(context: ChannelHandlerContext, error: Error) {
        // A refused WebSocket upgrade is reported here before the request itself arrives;
        // keep the connection so the router can answer it with 401.
        if error is NIOWebSocketUpgradeError { return }
        context.close(promise: nil)
    }
}

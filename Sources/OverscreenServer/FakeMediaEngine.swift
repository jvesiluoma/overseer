import Foundation
import OverscreenCore

/// Media engine without media: completes signaling with a placeholder offer.
/// Used by the Linux dev server and tests to exercise pairing, signaling and the web client UI.
public final class FakeMediaEngine: MediaEngine, @unchecked Sendable {
    public final class Session: MediaSession, @unchecked Sendable {
        public let request: MediaSessionRequest
        weak var delegate: MediaSessionDelegate?
        private let lock = NSLock()
        private var _received: [ClientMessage] = []
        private var _closed = false

        init(request: MediaSessionRequest, delegate: MediaSessionDelegate) {
            self.request = request
            self.delegate = delegate
        }

        public var received: [ClientMessage] { lock.sync { _received } }
        public var isClosed: Bool { lock.sync { _closed } }

        public func handle(_ message: ClientMessage) {
            lock.sync { _received.append(message) }
            if case .answer = message {
                let mode = DisplayModeCalculator.mode(for: request.viewport, config: request.display)
                delegate?.mediaSession(request.id, send: .status(state: .streaming, width: mode.pixelWidth, height: mode.pixelHeight))
            }
        }

        public func close() {
            lock.sync { _closed = true }
        }
    }

    private let lock = NSLock()
    private var _sessions: [Session] = []

    public init() {}

    public var sessions: [Session] { lock.sync { _sessions } }

    public static let placeholderOffer = "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=overscreen-fake\r\nt=0 0\r\n"

    public func makeSession(_ request: MediaSessionRequest, delegate: MediaSessionDelegate) throws -> MediaSession {
        let session = Session(request: request, delegate: delegate)
        lock.sync { _sessions.append(session) }
        delegate.mediaSession(request.id, send: .status(state: .preparing, width: nil, height: nil))
        delegate.mediaSession(request.id, send: .offer(sdp: FakeMediaEngine.placeholderOffer))
        return session
    }
}

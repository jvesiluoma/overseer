import Foundation
import OverscreenCore

/// What the signaling layer asks the media engine to start.
public struct MediaSessionRequest: Sendable {
    public var id: String
    public var display: DisplayConfig
    public var viewport: Viewport
    public var settings: HostSettings
    /// Number of sessions (including this one) sharing the bitrate budget.
    public var concurrentSessions: Int
}

/// Callbacks from a media session back to its signaling connection. Safe to call from any thread.
public protocol MediaSessionDelegate: AnyObject, Sendable {
    func mediaSession(_ id: String, send message: ServerMessage)
    func mediaSession(_ id: String, didReceiveStats stats: ClientStats)
    /// The engine ended the session on its own (capture failed, peer connection failed, ...).
    func mediaSessionDidEnd(_ id: String, reason: String)
}

/// One streaming peer. `handle` receives answer/candidate/viewport messages; everything else is
/// handled by the signaling layer.
public protocol MediaSession: AnyObject, Sendable {
    func handle(_ message: ClientMessage)
    func close()
}

/// Platform media stack (virtual display + capture + WebRTC on macOS; a fake one in tests).
public protocol MediaEngine: AnyObject, Sendable {
    func makeSession(_ request: MediaSessionRequest, delegate: MediaSessionDelegate) throws -> MediaSession
}

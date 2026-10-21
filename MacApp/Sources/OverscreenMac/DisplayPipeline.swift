import CoreVideo
import Foundation
import OverscreenCore
import WebRTC

/// Virtual display + capture + WebRTC video source for one configured display slot.
@MainActor
final class DisplayPipeline {
    let config: DisplayConfig
    let track: RTCVideoTrack
    private(set) var mode: DisplayMode
    private let source: RTCVideoSource
    private let videoCapturer: RTCVideoCapturer
    private var display: VirtualDisplay?
    private var capturer: ScreenCapturer?
    private var fps: Int
    private let boundsBox = LockedBox(DisplayRect(x: 0, y: 0, width: 1, height: 1))

    /// Global point frame of the display; readable from any thread (input arrives off-main).
    nonisolated var bounds: DisplayRect { boundsBox.value }

    var onTerminated: (() -> Void)?

    init(config: DisplayConfig, mode: DisplayMode, fps: Int, factory: RTCPeerConnectionFactory) {
        self.config = config
        self.mode = mode
        self.fps = fps
        let source = factory.videoSource(forScreenCast: true)
        self.source = source
        self.videoCapturer = RTCVideoCapturer(delegate: source)
        self.track = factory.videoTrack(with: source, trackId: "display-\(config.id)")
    }

    func start() async throws {
        guard Permissions.hasScreenRecording else { throw MacStreamingError.screenRecordingDenied }
        let display = try await VirtualDisplay.create(name: "Overscreen \(config.name)", slot: config.id, mode: mode) { [weak self] in
            self?.onTerminated?()
        }
        self.display = display
        let source = self.source
        let videoCapturer = self.videoCapturer
        let capturer = ScreenCapturer(
            onFrame: { pixelBuffer, timestampNs in
                let frame = RTCVideoFrame(buffer: RTCCVPixelBuffer(pixelBuffer: pixelBuffer), rotation: ._0, timeStampNs: timestampNs)
                source.capturer(videoCapturer, didCapture: frame)
            },
            onStop: { [weak self] _ in
                Task { @MainActor in self?.onTerminated?() }
            }
        )
        self.capturer = capturer
        try await applyCaptureSize()
    }

    /// Resizes the display in place; returns true if the mode changed.
    /// Resizes run one at a time, in call order.
    func resize(to newMode: DisplayMode) async throws -> Bool {
        let previous = lastResize
        let op = Task { @MainActor () throws -> Bool in
            await previous?.value
            guard newMode != self.mode, let display = self.display else { return false }
            try display.apply(newMode)
            self.mode = newMode
            try await self.applyCaptureSize()
            return true
        }
        lastResize = Task { _ = try? await op.value }
        return try await op.value
    }

    private var lastResize: Task<Void, Never>?

    func stop() async {
        await capturer?.stop()
        capturer = nil
        display?.invalidate()
        display = nil
    }

    private func applyCaptureSize() async throws {
        guard let display, let capturer else { return }
        // Give WindowServer a moment to settle the new mode before reading it back.
        try await Task.sleep(nanoseconds: 150_000_000)
        let size = display.pixelSize
        boundsBox.value = display.bounds
        source.adaptOutputFormat(toWidth: Int32(size.width), height: Int32(size.height), fps: Int32(fps))
        if capturerStarted {
            try await capturer.update(width: size.width, height: size.height, fps: fps)
        } else {
            capturerStarted = true
            try await capturer.start(displayID: display.displayID, width: size.width, height: size.height, fps: fps)
        }
    }

    private var capturerStarted = false

    var pixelSize: (width: Int, height: Int) { display?.pixelSize ?? (mode.pixelWidth, mode.pixelHeight) }
}

/// Minimal lock-protected value for cross-thread reads.
final class LockedBox<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var _value: Value

    init(_ value: Value) { _value = value }

    var value: Value {
        get { lock.sync { _value } }
        set { lock.sync { _value = newValue } }
    }
}

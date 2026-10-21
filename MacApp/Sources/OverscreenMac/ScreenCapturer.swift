import CoreMedia
import CoreVideo
import Foundation
import ScreenCaptureKit

/// Captures one display as NV12 pixel buffers with ScreenCaptureKit.
///
/// SCK only delivers frames when the screen changes. The last frame is re-sent at a low rate
/// while idle so that new viewers and keyframe requests (PLI) always get a picture.
final class ScreenCapturer: NSObject, SCStreamOutput, SCStreamDelegate, @unchecked Sendable {
    typealias FrameHandler = @Sendable (CVPixelBuffer, Int64) -> Void

    static let idleRepeatInterval: Double = 0.1

    private let queue = DispatchQueue(label: "org.overscreen.capture", qos: .userInteractive)
    // Lifecycle state; start/update/stop are only called from the main actor.
    @MainActor private var stream: SCStream?
    @MainActor private var repeatTimer: DispatchSourceTimer?
    @MainActor private var stopped = false
    // Only touched on `queue`.
    private var lastBuffer: CVPixelBuffer?
    private var lastDelivery: Double = 0

    private let onFrame: FrameHandler
    private let onStop: @Sendable (Error) -> Void

    init(onFrame: @escaping FrameHandler, onStop: @escaping @Sendable (Error) -> Void) {
        self.onFrame = onFrame
        self.onStop = onStop
    }

    @MainActor
    func start(displayID: CGDirectDisplayID, width: Int, height: Int, fps: Int) async throws {
        let display = try await Self.findDisplay(displayID)
        guard !stopped else { return }
        let filter = SCContentFilter(display: display, excludingWindows: [])
        let stream = SCStream(filter: filter, configuration: Self.configuration(width: width, height: height, fps: fps), delegate: self)
        try stream.addStreamOutput(self, type: .screen, sampleHandlerQueue: queue)
        self.stream = stream
        try await stream.startCapture()
        // stop() may have run while we were waiting; it already cleared `stream`.
        if stopped {
            try? await stream.stopCapture()
            return
        }
        startRepeater()
    }

    @MainActor
    func update(width: Int, height: Int, fps: Int) async throws {
        try await stream?.updateConfiguration(Self.configuration(width: width, height: height, fps: fps))
        queue.async { self.lastBuffer = nil }
    }

    @MainActor
    func stop() async {
        stopped = true
        repeatTimer?.cancel()
        repeatTimer = nil
        let stream = self.stream
        self.stream = nil
        if let stream {
            try? await stream.stopCapture()
        }
        queue.async { self.lastBuffer = nil }
    }

    // MARK: SCStreamOutput

    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen, sampleBuffer.isValid,
              let attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
              let rawStatus = attachments.first?[.status] as? Int,
              SCFrameStatus(rawValue: rawStatus) == .complete,
              let pixelBuffer = CMSampleBufferGetImageBuffer(sampleBuffer)
        else { return }
        deliver(pixelBuffer)
    }

    // MARK: SCStreamDelegate

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        onStop(error)
    }

    // MARK: Private

    private func deliver(_ buffer: CVPixelBuffer) {
        lastBuffer = buffer
        lastDelivery = ProcessInfo.processInfo.systemUptime
        onFrame(buffer, Self.nowNanoseconds())
    }

    @MainActor
    private func startRepeater() {
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + Self.idleRepeatInterval, repeating: Self.idleRepeatInterval)
        timer.setEventHandler { [weak self] in
            guard let self, let buffer = self.lastBuffer else { return }
            if ProcessInfo.processInfo.systemUptime - self.lastDelivery >= Self.idleRepeatInterval {
                self.deliver(buffer)
            }
        }
        timer.resume()
        repeatTimer = timer
    }

    private static func configuration(width: Int, height: Int, fps: Int) -> SCStreamConfiguration {
        let config = SCStreamConfiguration()
        config.width = width
        config.height = height
        config.pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange // NV12, what VideoToolbox wants
        config.minimumFrameInterval = CMTime(value: 1, timescale: CMTimeScale(max(fps, 1)))
        config.queueDepth = 5
        config.showsCursor = true
        config.capturesAudio = false
        return config
    }

    /// A freshly created virtual display takes a moment to show up in SCShareableContent.
    private static func findDisplay(_ id: CGDirectDisplayID) async throws -> SCDisplay {
        for _ in 0..<30 {
            let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
            if let display = content.displays.first(where: { $0.displayID == id }) { return display }
            try await Task.sleep(nanoseconds: 100_000_000)
        }
        throw MacStreamingError.displayNotCapturable
    }

    static func nowNanoseconds() -> Int64 {
        Int64(clock_gettime_nsec_np(CLOCK_UPTIME_RAW))
    }
}

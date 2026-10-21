import Foundation
import OverscreenCore
import OverscreenServer
import WebRTC

/// libwebrtc-based media engine: one virtual display pipeline per display slot, one peer per session.
public final class MacMediaEngine: MediaEngine, @unchecked Sendable {
    let factory: RTCPeerConnectionFactory
    let injector = InputInjector()
    private let settingsBox: LockedBox<HostSettings>

    // Pipeline bookkeeping, all on the main actor. Users are counted per pipeline object so a
    // replaced pipeline's late releases can never affect its successor.
    @MainActor private var pipelines: [Int: DisplayPipeline] = [:]
    @MainActor private var users: [ObjectIdentifier: Int] = [:]
    @MainActor private var starting: [Int: Task<DisplayPipeline, Error>] = [:]
    @MainActor private var lingerTasks: [Int: Task<Void, Never>] = [:]
    /// Bumped by `shutdown` so pipelines that finish starting afterwards are discarded.
    @MainActor private var generation = 0

    /// Keep an unused display alive briefly so a reconnecting client doesn't make windows jump.
    static let lingerSeconds: UInt64 = 15

    public init(settings: HostSettings) {
        RTCInitializeSSL()
        factory = RTCPeerConnectionFactory(encoderFactory: PreferredEncoderFactory(), decoderFactory: RTCDefaultVideoDecoderFactory())
        settingsBox = LockedBox(settings)
    }

    /// Live settings (input toggles etc.) read by sessions.
    public var settings: HostSettings {
        get { settingsBox.value }
        set { settingsBox.value = newValue }
    }

    public func makeSession(_ request: MediaSessionRequest, delegate: MediaSessionDelegate) throws -> MediaSession {
        let session = MacMediaSession(request: request, delegate: delegate, engine: self)
        session.start()
        return session
    }

    /// Returns a started pipeline for the request's display slot and counts the caller as a user.
    /// The caller must hand it back with `releasePipeline(_:)`.
    @MainActor
    func acquirePipeline(for request: MediaSessionRequest) async throws -> DisplayPipeline {
        let index = request.display.id
        lingerTasks.removeValue(forKey: index)?.cancel()
        let pipeline = try await startedPipeline(for: request)
        users[ObjectIdentifier(pipeline), default: 0] += 1
        // Fit the display to this viewer; a failed resize keeps the current mode.
        let wanted = DisplayModeCalculator.mode(for: request.viewport, config: request.display)
        _ = try? await pipeline.resize(to: wanted)
        return pipeline
    }

    @MainActor
    private func startedPipeline(for request: MediaSessionRequest) async throws -> DisplayPipeline {
        let index = request.display.id
        if let inFlight = starting[index] { return try await inFlight.value }
        if let existing = pipelines[index] {
            let inUse = (users[ObjectIdentifier(existing)] ?? 0) > 0
            // Settings changes (e.g. HiDPI) only take effect once nobody is watching.
            if inUse || existing.config.hiDPI == request.display.hiDPI { return existing }
            pipelines[index] = nil
            await existing.stop()
            // Someone may have started a replacement while we were stopping.
            if let inFlight = starting[index] { return try await inFlight.value }
            if let replacement = pipelines[index] { return replacement }
        }
        let mode = DisplayModeCalculator.mode(for: request.viewport, config: request.display)
        let fps = request.settings.fps
        let factory = self.factory
        let config = request.display
        let startedGeneration = generation
        let task = Task { @MainActor () throws -> DisplayPipeline in
            let pipeline = DisplayPipeline(config: config, mode: mode, fps: fps, factory: factory)
            do {
                try await pipeline.start()
            } catch {
                await pipeline.stop()
                throw error
            }
            return pipeline
        }
        starting[index] = task
        defer { starting[index] = nil }
        let pipeline = try await task.value
        guard generation == startedGeneration else {
            await pipeline.stop()
            throw CancellationError()
        }
        pipeline.onTerminated = { [weak self, weak pipeline] in
            guard let self, let pipeline else { return }
            Task { @MainActor in
                if self.pipelines[index] === pipeline { self.pipelines[index] = nil }
                await pipeline.stop()
            }
        }
        pipelines[index] = pipeline
        return pipeline
    }

    @MainActor
    func releasePipeline(_ pipeline: DisplayPipeline) {
        let id = ObjectIdentifier(pipeline)
        guard let count = users[id] else { return }
        if count > 1 {
            users[id] = count - 1
            return
        }
        users[id] = nil
        let index = pipeline.config.id
        guard pipelines[index] === pipeline else {
            // Already replaced or shut down: nothing else will stop it.
            Task { @MainActor in await pipeline.stop() }
            return
        }
        lingerTasks[index]?.cancel()
        lingerTasks[index] = Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: Self.lingerSeconds * 1_000_000_000)
            guard let self, !Task.isCancelled else { return }
            self.lingerTasks[index] = nil
            guard self.pipelines[index] === pipeline, self.users[id] == nil else { return }
            self.pipelines[index] = nil
            await pipeline.stop()
        }
    }

    /// Tears down every display immediately (host stopped).
    @MainActor
    public func shutdown() async {
        generation += 1
        for task in lingerTasks.values { task.cancel() }
        lingerTasks.removeAll()
        let all = Array(pipelines.values)
        pipelines.removeAll()
        users.removeAll()
        for p in all { await p.stop() }
    }
}

/// Offers H.264 Constrained Baseline (VideoToolbox) first, VP8 as a fallback for browsers
/// without H.264. Baseline decodes cheaply and has no B-frames, which matters on car browsers.
final class PreferredEncoderFactory: NSObject, RTCVideoEncoderFactory {
    private let base = RTCDefaultVideoEncoderFactory()

    func createEncoder(_ info: RTCVideoCodecInfo) -> RTCVideoEncoder? {
        base.createEncoder(info)
    }

    func supportedCodecs() -> [RTCVideoCodecInfo] {
        let all = base.supportedCodecs()
        let baseline = all.filter { $0.name == kRTCVideoCodecH264Name && ($0.parameters["profile-level-id"] ?? "").hasPrefix("42e0") }
        let vp8 = all.filter { $0.name == "VP8" } // no exported constant for VP8
        return baseline + vp8
    }
}

import Foundation
import OverscreenCore
import OverscreenServer
@preconcurrency import WebRTC

/// One browser: a send-only video peer plus the "input" data channel.
final class MacMediaSession: NSObject, MediaSession, @unchecked Sendable {
    let request: MediaSessionRequest
    private weak var delegate: MediaSessionDelegate?
    private let engine: MacMediaEngine
    /// Serializes all peer connection state.
    private let queue = DispatchQueue(label: "org.overscreen.session")

    private var peer: RTCPeerConnection?
    private var sender: RTCRtpSender?
    private var dataChannel: RTCDataChannel?
    private var pipeline: DisplayPipeline?
    private var pendingCandidates: [IceCandidate] = []
    private var remoteDescriptionSet = false
    /// Host candidates gathered before the offer went out; the browser needs the offer first.
    private var offerSent = false
    private var pendingLocalCandidates: [IceCandidate] = []
    private var closed = false
    private var mapper: InputMapper?

    init(request: MediaSessionRequest, delegate: MediaSessionDelegate, engine: MacMediaEngine) {
        self.request = request
        self.delegate = delegate
        self.engine = engine
    }

    func start() {
        delegate?.mediaSession(request.id, send: .status(state: .preparing, width: nil, height: nil))
        Task { @MainActor in
            do {
                let pipeline = try await engine.acquirePipeline(for: request)
                let size = pipeline.pixelSize
                let bounds = pipeline.bounds
                let track = pipeline.track
                queue.async { self.createPeer(pipeline: pipeline, track: track, pixelSize: size, bounds: bounds) }
            } catch {
                end(reason: "display_failed: \(error)")
            }
        }
    }

    // MARK: MediaSession

    func handle(_ message: ClientMessage) {
        switch message {
        case .answer(let sdp):
            queue.async { self.applyAnswer(sdp) }
        case .candidate(let candidate):
            queue.async { self.addCandidate(candidate) }
        case .viewport(let viewport):
            resize(to: viewport)
        default:
            break
        }
    }

    func close() {
        queue.async {
            guard !self.closed else { return }
            self.closed = true
            if let mapper = self.mapper { self.engine.injector.perform(mapper.reset()) }
            self.dataChannel?.close()
            self.peer?.close()
            self.peer = nil
            if let pipeline = self.pipeline {
                self.pipeline = nil
                Task { @MainActor in self.engine.releasePipeline(pipeline) }
            }
        }
    }

    // MARK: Peer setup (on `queue`)

    private func createPeer(pipeline: DisplayPipeline, track: RTCVideoTrack, pixelSize: (width: Int, height: Int), bounds: DisplayRect) {
        guard !closed else {
            Task { @MainActor in self.engine.releasePipeline(pipeline) }
            return
        }
        self.pipeline = pipeline
        let mapper = InputMapper(bounds: bounds)
        mapper.invertScroll = engine.settings.invertScroll
        self.mapper = mapper

        let config = RTCConfiguration()
        config.iceServers = [] // LAN only: host candidates, no STUN/TURN
        config.sdpSemantics = .unifiedPlan
        config.bundlePolicy = .maxBundle
        config.rtcpMuxPolicy = .require
        config.continualGatheringPolicy = .gatherContinually
        let constraints = RTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil)
        let created: RTCPeerConnection? = engine.factory.peerConnection(with: config, constraints: constraints, delegate: self)
        guard let peer = created else { return end(reason: "peer_failed") }
        self.peer = peer

        let transceiverInit = RTCRtpTransceiverInit()
        transceiverInit.direction = .sendOnly
        transceiverInit.streamIds = ["overscreen"]
        let transceiver: RTCRtpTransceiver? = peer.addTransceiver(with: track, init: transceiverInit)
        sender = transceiver?.sender

        let channelConfig = RTCDataChannelConfiguration()
        channelConfig.isOrdered = true
        let channel: RTCDataChannel? = peer.dataChannel(forLabel: "input", configuration: channelConfig)
        channel?.delegate = self
        dataChannel = channel

        peer.offer(for: constraints) { [weak self] sdp, error in
            guard let self else { return }
            self.queue.async {
                guard let sdp, error == nil, let peer = self.peer else {
                    return self.end(reason: "offer_failed: \(error?.localizedDescription ?? "unknown")")
                }
                peer.setLocalDescription(sdp) { error in
                    self.queue.async {
                        if let error { return self.end(reason: "local_description_failed: \(error.localizedDescription)") }
                        self.applyEncodingParameters(pixelSize: pixelSize)
                        self.delegate?.mediaSession(self.request.id, send: .offer(sdp: sdp.sdp))
                        self.offerSent = true
                        for c in self.pendingLocalCandidates { self.delegate?.mediaSession(self.request.id, send: .candidate(c)) }
                        self.pendingLocalCandidates.removeAll()
                    }
                }
            }
        }
    }

    private func applyEncodingParameters(pixelSize: (width: Int, height: Int)) {
        guard let sender else { return }
        let settings = engine.settings
        let mode = DisplayMode(pixelWidth: pixelSize.width, pixelHeight: pixelSize.height, hiDPI: false)
        let bitrate = BitratePolicy.bitrate(for: mode, fps: settings.fps, maxMbps: settings.maxBitrateMbps,
                                            sessions: request.concurrentSessions)
        let parameters = sender.parameters
        for encoding in parameters.encodings {
            encoding.isActive = true
            encoding.maxBitrateBps = NSNumber(value: bitrate)
            encoding.maxFramerate = NSNumber(value: settings.fps)
        }
        // Text must stay sharp: drop frames under congestion rather than resolution.
        parameters.degradationPreference = NSNumber(value: RTCDegradationPreference.maintainResolution.rawValue)
        sender.parameters = parameters
    }

    private func applyAnswer(_ sdp: String) {
        guard let peer, !closed else { return }
        peer.setRemoteDescription(RTCSessionDescription(type: .answer, sdp: sdp)) { [weak self] error in
            guard let self else { return }
            self.queue.async {
                if let error { return self.end(reason: "answer_rejected: \(error.localizedDescription)") }
                self.remoteDescriptionSet = true
                let pending = self.pendingCandidates
                self.pendingCandidates.removeAll()
                pending.forEach(self.addCandidate)
            }
        }
    }

    private func addCandidate(_ candidate: IceCandidate) {
        guard let peer, !closed else { return }
        guard remoteDescriptionSet else {
            if pendingCandidates.count < 64 { pendingCandidates.append(candidate) }
            return
        }
        let rtc = RTCIceCandidate(sdp: candidate.candidate, sdpMLineIndex: candidate.sdpMLineIndex, sdpMid: candidate.sdpMid)
        peer.add(rtc) { _ in }
    }

    private func resize(to viewport: Viewport) {
        let wanted = DisplayModeCalculator.mode(for: viewport, config: request.display)
        queue.async {
            guard let pipeline = self.pipeline, !self.closed else { return }
            Task { @MainActor in await self.resize(pipeline: pipeline, to: wanted) }
        }
    }

    @MainActor
    private func resize(pipeline: DisplayPipeline, to wanted: DisplayMode) async {
        do {
            guard try await pipeline.resize(to: wanted) else { return }
            let size = pipeline.pixelSize
            let bounds = pipeline.bounds
            queue.async {
                self.mapper?.bounds = bounds
                self.applyEncodingParameters(pixelSize: size)
            }
            delegate?.mediaSession(request.id, send: .status(state: .streaming, width: size.width, height: size.height))
        } catch {
            delegate?.mediaSession(request.id, send: .error(code: "resize_failed", message: "\(error)"))
        }
    }

    private func end(reason: String) {
        delegate?.mediaSessionDidEnd(request.id, reason: reason)
        close()
    }

    fileprivate func handleInput(_ data: Data) {
        guard let event = try? InputCodec.decode(data) else { return }
        if case .stats(let stats) = event {
            delegate?.mediaSession(request.id, didReceiveStats: stats)
            return
        }
        let settings = engine.settings
        guard settings.allowInput, let mapper, let pipeline else { return }
        mapper.bounds = pipeline.bounds
        mapper.invertScroll = settings.invertScroll
        engine.injector.perform(mapper.handle(event))
    }
}

extension MacMediaSession: RTCPeerConnectionDelegate {
    func peerConnection(_ peerConnection: RTCPeerConnection, didChange stateChanged: RTCSignalingState) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didAdd stream: RTCMediaStream) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didRemove stream: RTCMediaStream) {}
    func peerConnectionShouldNegotiate(_ peerConnection: RTCPeerConnection) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didChange newState: RTCIceGatheringState) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didRemove candidates: [RTCIceCandidate]) {}

    func peerConnection(_ peerConnection: RTCPeerConnection, didChange newState: RTCIceConnectionState) {
        if newState == .connected || newState == .completed {
            queue.async {
                guard let pipeline = self.pipeline else { return }
                Task { @MainActor in
                    let size = pipeline.pixelSize
                    self.delegate?.mediaSession(self.request.id, send: .status(state: .streaming, width: size.width, height: size.height))
                }
            }
        } else if newState == .failed {
            queue.async { self.end(reason: "ice_failed") }
        }
    }

    func peerConnection(_ peerConnection: RTCPeerConnection, didGenerate candidate: RTCIceCandidate) {
        let c = IceCandidate(candidate: candidate.sdp, sdpMid: candidate.sdpMid, sdpMLineIndex: candidate.sdpMLineIndex)
        queue.async {
            if self.offerSent {
                self.delegate?.mediaSession(self.request.id, send: .candidate(c))
            } else {
                self.pendingLocalCandidates.append(c)
            }
        }
    }

    func peerConnection(_ peerConnection: RTCPeerConnection, didOpen dataChannel: RTCDataChannel) {
        // A client-created channel labelled "input" is accepted too.
        if dataChannel.label == "input" {
            dataChannel.delegate = self
            queue.async { if self.dataChannel == nil { self.dataChannel = dataChannel } }
        }
    }
}

extension MacMediaSession: RTCDataChannelDelegate {
    func dataChannelDidChangeState(_ dataChannel: RTCDataChannel) {}

    func dataChannel(_ dataChannel: RTCDataChannel, didReceiveMessageWith buffer: RTCDataBuffer) {
        guard !buffer.isBinary else { return }
        let data = buffer.data
        queue.async { self.handleInput(data) }
    }
}

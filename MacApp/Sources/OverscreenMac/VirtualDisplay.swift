import CoreGraphics
import CVirtualDisplay
import Foundation
import OverscreenCore

public enum MacStreamingError: Error, CustomStringConvertible {
    case virtualDisplayUnsupported
    case virtualDisplayCreationFailed
    case modeRejected(DisplayMode)
    case displayNotOnline
    case displayNotCapturable
    case screenRecordingDenied
    case peerConnectionFailed(String)

    public var description: String {
        switch self {
        case .virtualDisplayUnsupported: return "This macOS version does not offer virtual displays (CGVirtualDisplay)."
        case .virtualDisplayCreationFailed: return "macOS refused to create the virtual display."
        case .modeRejected(let m): return "macOS rejected the display mode \(m.pixelWidth)×\(m.pixelHeight)."
        case .displayNotOnline: return "macOS created the virtual display but never brought it online."
        case .displayNotCapturable: return "The virtual display did not appear in ScreenCaptureKit."
        case .screenRecordingDenied: return "Screen Recording permission is required."
        case .peerConnectionFailed(let why): return "WebRTC error: \(why)"
        }
    }
}

/// A virtual monitor that exists while this object is alive.
@MainActor
final class VirtualDisplay {
    private let impl: OVSVirtualDisplay
    private(set) var mode: DisplayMode

    var displayID: CGDirectDisplayID { impl.displayID }

    /// Serial numbers of different identities for one slot are this far apart.
    private static let generationStride: UInt32 = 0x100
    private static let maxGenerations = 4

    /// Creates the display for `slot` and waits until WindowServer brings it online.
    ///
    /// macOS keys saved arrangements on vendor/product/serial and can get stuck on one identity:
    /// it then accepts the display but never attaches it. In that case the slot moves on to a fresh
    /// serial number, and the one that works is remembered.
    static func create(name: String, slot: Int, mode: DisplayMode, onTerminate: @escaping () -> Void) async throws -> VirtualDisplay {
        let key = "VirtualDisplayGeneration.\(slot)"
        let saved = UserDefaults.standard.integer(forKey: key)
        for attempt in 0..<maxGenerations {
            let generation = (saved + attempt) % maxGenerations
            let serial = UInt32(slot + 1) + UInt32(generation) * generationStride
            let display = try VirtualDisplay(name: name, serial: serial, mode: mode)
            if await display.waitUntilOnline() {
                if generation != saved { UserDefaults.standard.set(generation, forKey: key) }
                display.impl.terminationHandler = onTerminate
                return display
            }
            display.invalidate()
        }
        throw MacStreamingError.displayNotOnline
    }

    private init(name: String, serial: UInt32, mode: DisplayMode) throws {
        guard OVSVirtualDisplay.isSupported() else { throw MacStreamingError.virtualDisplayUnsupported }
        let maxW = DisplayModeCalculator.maxPixelWidth, maxH = DisplayModeCalculator.maxPixelHeight
        // Physical size only influences what macOS reports as PPI; ~110 ppi like an external monitor.
        let mm = CGSize(width: Double(maxW) / 110 * 25.4, height: Double(maxH) / 110 * 25.4)
        guard let impl = OVSVirtualDisplay(name: name, serial: serial, maxPixelsWide: UInt32(maxW),
                                           maxPixelsHigh: UInt32(maxH), sizeInMillimeters: mm)
        else { throw MacStreamingError.virtualDisplayCreationFailed }
        self.impl = impl
        self.mode = mode
        try apply(mode)
    }

    private func waitUntilOnline() async -> Bool {
        for _ in 0..<20 {
            var ids = [CGDirectDisplayID](repeating: 0, count: 32)
            var count: UInt32 = 0
            if CGGetOnlineDisplayList(UInt32(ids.count), &ids, &count) == .success,
               ids.prefix(Int(count)).contains(displayID) {
                return true
            }
            try? await Task.sleep(nanoseconds: 100_000_000)
        }
        return false
    }

    func apply(_ mode: DisplayMode) throws {
        guard impl.applyMode(withPixelWidth: UInt32(mode.pixelWidth), pixelHeight: UInt32(mode.pixelHeight),
                             refreshRate: 60, hiDPI: mode.hiDPI)
        else { throw MacStreamingError.modeRejected(mode) }
        self.mode = mode
        selectBestSystemMode(for: mode)
        // WindowServer may still list the old modes right after applySettings; try again shortly.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) { [weak self] in
            guard let self, self.mode == mode else { return }
            self.selectBestSystemMode(for: mode)
        }
    }

    /// With hiDPI the display offers both a 1x and a 2x variant of the same pixel size;
    /// pick the one matching the requested point size through the public display API.
    private func selectBestSystemMode(for mode: DisplayMode) {
        let options = [kCGDisplayShowDuplicateLowResolutionModes: kCFBooleanTrue] as CFDictionary
        guard let modes = CGDisplayCopyAllDisplayModes(displayID, options) as? [CGDisplayMode] else { return }
        let match = modes.first { $0.pixelWidth == mode.pixelWidth && $0.width == mode.pointWidth }
            ?? modes.first { $0.pixelWidth == mode.pixelWidth }
        guard let match else { return }
        if let current = CGDisplayCopyDisplayMode(displayID), current.pixelWidth == match.pixelWidth, current.width == match.width {
            return
        }
        var config: CGDisplayConfigRef?
        guard CGBeginDisplayConfiguration(&config) == .success else { return }
        CGConfigureDisplayWithDisplayMode(config, displayID, match, nil)
        CGCompleteDisplayConfiguration(config, .forSession)
    }

    /// Frame in global points (what CGEvent coordinates use).
    var bounds: DisplayRect {
        let b = CGDisplayBounds(displayID)
        return DisplayRect(x: b.origin.x, y: b.origin.y, width: b.width, height: b.height)
    }

    /// Size of the framebuffer actually in use (may differ from the request if macOS adjusted it).
    var pixelSize: (width: Int, height: Int) {
        if let m = CGDisplayCopyDisplayMode(displayID) { return (m.pixelWidth, m.pixelHeight) }
        return (mode.pixelWidth, mode.pixelHeight)
    }

    func invalidate() {
        impl.terminationHandler = nil
        impl.invalidate()
    }
}

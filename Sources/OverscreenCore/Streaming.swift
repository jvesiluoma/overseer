import Foundation

public struct DisplayMode: Equatable, Sendable {
    /// Size of the captured and encoded frames.
    public var pixelWidth: Int
    public var pixelHeight: Int
    public var hiDPI: Bool

    public init(pixelWidth: Int, pixelHeight: Int, hiDPI: Bool) {
        self.pixelWidth = pixelWidth
        self.pixelHeight = pixelHeight
        self.hiDPI = hiDPI
    }

    /// Logical size macOS lays out the desktop in.
    public var pointWidth: Int { hiDPI ? pixelWidth / 2 : pixelWidth }
    public var pointHeight: Int { hiDPI ? pixelHeight / 2 : pixelHeight }
}

public enum DisplayModeCalculator {
    public static let maxPixelWidth = 3840
    public static let maxPixelHeight = 2400
    public static let minPixelWidth = 640
    public static let minPixelHeight = 480
    public static let fallbackSize = (width: 1920, height: 1200)

    public static func mode(for viewport: Viewport?, config: DisplayConfig) -> DisplayMode {
        var width: Double
        var height: Double
        switch config.resolution {
        case .custom(let w, let h):
            width = Double(w)
            height = Double(h)
        case .auto:
            if let v = viewport, v.isPlausible {
                width = v.width * v.dpr
                height = v.height * v.dpr
            } else {
                width = Double(fallbackSize.width)
                height = Double(fallbackSize.height)
            }
        }
        // Scale into limits while keeping the aspect ratio.
        let down = min(1, Double(maxPixelWidth) / width, Double(maxPixelHeight) / height)
        width *= down
        height *= down
        let up = max(1, Double(minPixelWidth) / width, Double(minPixelHeight) / height)
        width = min(width * up, Double(maxPixelWidth))
        height = min(height * up, Double(maxPixelHeight))
        // H.264 4:2:0 needs even sizes; HiDPI also needs an even point size.
        let multiple = config.hiDPI ? 4 : 2
        func align(_ v: Double) -> Int { max(multiple, Int(v) / multiple * multiple) }
        return DisplayMode(pixelWidth: align(width), pixelHeight: align(height), hiDPI: config.hiDPI)
    }
}

public enum BitratePolicy {
    public static let floorBps = 1_500_000

    /// Bitrate for one session: enough for the resolution, capped by the user's maximum
    /// shared across concurrent sessions, never below the floor.
    public static func bitrate(for mode: DisplayMode, fps: Int, maxMbps: Double, sessions: Int) -> Int {
        let cap = Int(maxMbps * 1_000_000) / max(sessions, 1)
        // ~0.1 bit per pixel per frame is plenty for desktop content in H.264.
        let wanted = Int(Double(mode.pixelWidth * mode.pixelHeight * fps) * 0.1)
        return max(min(wanted, cap), floorBps)
    }
}

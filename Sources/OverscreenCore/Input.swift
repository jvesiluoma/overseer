import Foundation

/// Events the browser sends over the "input" DataChannel.
/// Coordinates are normalized to the visible video content: (0,0) top-left, (1,1) bottom-right.
public enum InputEvent: Equatable, Sendable {
    case down(x: Double, y: Double)
    case move(x: Double, y: Double)
    case up(x: Double, y: Double)
    /// dx/dy are the finger (content) movement as a fraction of the content size.
    case scroll(x: Double, y: Double, dx: Double, dy: Double)
    case rightClick(x: Double, y: Double)
    case stats(ClientStats)
}

public struct ClientStats: Codable, Equatable, Sendable {
    public var rttMs: Double?
    public var jitterMs: Double?
    public var decodeMs: Double?
    public var fps: Double?

    public init(rttMs: Double? = nil, jitterMs: Double? = nil, decodeMs: Double? = nil, fps: Double? = nil) {
        self.rttMs = rttMs
        self.jitterMs = jitterMs
        self.decodeMs = decodeMs
        self.fps = fps
    }
}

private struct WireInput: Decodable {
    var t: String
    var x: Double?
    var y: Double?
    var dx: Double?
    var dy: Double?
    var rtt: Double?
    var jitter: Double?
    var decode: Double?
    var fps: Double?
}

public enum InputCodec {
    public static let maxMessageBytes = 1024

    public static func decode(_ data: Data) throws -> InputEvent {
        guard data.count <= maxMessageBytes else { throw ProtocolError.invalidValue("size") }
        let wire: WireInput
        do {
            wire = try JSONDecoder().decode(WireInput.self, from: data)
        } catch {
            throw ProtocolError.malformed
        }
        func coord(_ v: Double?, _ name: String) throws -> Double {
            guard let v else { throw ProtocolError.missingField(name) }
            guard v.isFinite else { throw ProtocolError.invalidValue(name) }
            return v
        }
        switch wire.t {
        case "down": return .down(x: try coord(wire.x, "x"), y: try coord(wire.y, "y"))
        case "move": return .move(x: try coord(wire.x, "x"), y: try coord(wire.y, "y"))
        case "up": return .up(x: try coord(wire.x, "x"), y: try coord(wire.y, "y"))
        case "scroll":
            return .scroll(x: try coord(wire.x, "x"), y: try coord(wire.y, "y"),
                           dx: try coord(wire.dx ?? 0, "dx"), dy: try coord(wire.dy ?? 0, "dy"))
        case "rightclick": return .rightClick(x: try coord(wire.x, "x"), y: try coord(wire.y, "y"))
        case "stats":
            func finite(_ v: Double?) -> Double? { v.flatMap { $0.isFinite ? $0 : nil } }
            return .stats(ClientStats(rttMs: finite(wire.rtt), jitterMs: finite(wire.jitter),
                                      decodeMs: finite(wire.decode), fps: finite(wire.fps)))
        default:
            throw ProtocolError.unknownType(wire.t)
        }
    }
}

public struct Point: Equatable, Sendable {
    public var x: Double
    public var y: Double

    public init(x: Double, y: Double) {
        self.x = x
        self.y = y
    }
}

/// A display's frame in global (top-left origin) point coordinates, as CGDisplayBounds reports it.
public struct DisplayRect: Equatable, Sendable {
    public var x: Double
    public var y: Double
    public var width: Double
    public var height: Double

    public init(x: Double, y: Double, width: Double, height: Double) {
        self.x = x
        self.y = y
        self.width = width
        self.height = height
    }
}

/// Platform-neutral mouse actions; the macOS layer turns each into a CGEvent.
public enum MouseAction: Equatable, Sendable {
    case move(Point)
    case drag(Point)
    case leftDown(Point, clickCount: Int)
    case leftUp(Point, clickCount: Int)
    case rightClick(Point)
    /// Pixel deltas; positive dy moves content down (scrolls towards the top), like a finger pulling down.
    case scroll(dx: Int, dy: Int)
}

/// Turns normalized browser input into mouse actions confined to one display.
/// Counts multi-clicks itself (time + distance), the way a native mouse driver would.
public final class InputMapper {
    public var bounds: DisplayRect
    public var invertScroll = false
    public var multiClickInterval: Double = 0.4
    /// Max distance between taps of a multi-click, as a fraction of the display's larger side.
    public var multiClickDistance: Double = 0.03

    private let clock: () -> Double
    private var leftHeld = false
    private var lastPoint: Point?
    private var lastDownTime: Double = -.infinity
    private var lastDownPoint = Point(x: 0, y: 0)
    private var clickCount = 0

    public init(bounds: DisplayRect, clock: @escaping () -> Double = { ProcessInfo.processInfo.systemUptime }) {
        self.bounds = bounds
        self.clock = clock
    }

    public func point(x: Double, y: Double) -> Point {
        func clamp(_ v: Double) -> Double { min(max(v, 0), 1) }
        // Stay strictly inside the display so the cursor never lands on a neighbour.
        let px = bounds.x + min(clamp(x) * bounds.width, max(bounds.width - 1, 0))
        let py = bounds.y + min(clamp(y) * bounds.height, max(bounds.height - 1, 0))
        return Point(x: px.rounded(), y: py.rounded())
    }

    public func handle(_ event: InputEvent) -> [MouseAction] {
        switch event {
        case .down(let x, let y):
            let p = point(x: x, y: y)
            var actions = releaseIfHeld(at: p)
            let now = clock()
            let threshold = max(12, multiClickDistance * max(bounds.width, bounds.height))
            if now - lastDownTime <= multiClickInterval && distance(p, lastDownPoint) <= threshold {
                clickCount = min(clickCount + 1, 3)
            } else {
                clickCount = 1
            }
            lastDownTime = now
            lastDownPoint = p
            leftHeld = true
            lastPoint = p
            actions.append(.move(p))
            actions.append(.leftDown(p, clickCount: clickCount))
            return actions
        case .move(let x, let y):
            let p = point(x: x, y: y)
            guard p != lastPoint else { return [] }
            lastPoint = p
            return [leftHeld ? .drag(p) : .move(p)]
        case .up(let x, let y):
            let p = point(x: x, y: y)
            lastPoint = p
            guard leftHeld else { return [.move(p)] }
            leftHeld = false
            return [.leftUp(p, clickCount: clickCount)]
        case .scroll(let x, let y, let dx, let dy):
            let p = point(x: x, y: y)
            var actions = releaseIfHeld(at: p)
            if p != lastPoint {
                actions.append(.move(p))
                lastPoint = p
            }
            let sign: Double = invertScroll ? -1 : 1
            let sx = Int((dx * bounds.width * sign).rounded())
            let sy = Int((dy * bounds.height * sign).rounded())
            if sx != 0 || sy != 0 {
                actions.append(.scroll(dx: sx, dy: sy))
            }
            return actions
        case .rightClick(let x, let y):
            let p = point(x: x, y: y)
            var actions = releaseIfHeld(at: p)
            lastPoint = p
            actions.append(.rightClick(p))
            return actions
        case .stats:
            return []
        }
    }

    /// Releases a held button, e.g. when the session ends mid-drag.
    public func reset() -> [MouseAction] {
        guard leftHeld, let p = lastPoint else { return [] }
        leftHeld = false
        return [.leftUp(p, clickCount: clickCount)]
    }

    private func releaseIfHeld(at p: Point) -> [MouseAction] {
        guard leftHeld else { return [] }
        leftHeld = false
        return [.leftUp(lastPoint ?? p, clickCount: clickCount)]
    }

    private func distance(_ a: Point, _ b: Point) -> Double {
        ((a.x - b.x) * (a.x - b.x) + (a.y - b.y) * (a.y - b.y)).squareRoot()
    }
}

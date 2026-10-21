import ApplicationServices
import CoreGraphics
import Foundation
import OverscreenCore

/// Posts mouse events for actions produced by `InputMapper`. Needs Accessibility permission.
final class InputInjector: @unchecked Sendable {
    private let source = CGEventSource(stateID: .hidSystemState)

    func perform(_ actions: [MouseAction]) {
        for action in actions { post(action) }
    }

    private func post(_ action: MouseAction) {
        switch action {
        case .move(let p):
            mouse(.mouseMoved, at: p, button: .left)
        case .drag(let p):
            mouse(.leftMouseDragged, at: p, button: .left)
        case .leftDown(let p, let clicks):
            mouse(.leftMouseDown, at: p, button: .left, clicks: clicks)
        case .leftUp(let p, let clicks):
            mouse(.leftMouseUp, at: p, button: .left, clicks: clicks)
        case .rightClick(let p):
            mouse(.rightMouseDown, at: p, button: .right)
            mouse(.rightMouseUp, at: p, button: .right)
        case .scroll(let dx, let dy):
            let event = CGEvent(scrollWheelEvent2Source: source, units: .pixel, wheelCount: 2,
                                wheel1: Int32(clamping: dy), wheel2: Int32(clamping: dx), wheel3: 0)
            event?.post(tap: .cghidEventTap)
        }
    }

    // Qualified: ApplicationServices also exports a legacy `Point` type.
    private func mouse(_ type: CGEventType, at p: OverscreenCore.Point, button: CGMouseButton, clicks: Int = 1) {
        guard let event = CGEvent(mouseEventSource: source, mouseType: type,
                                  mouseCursorPosition: CGPoint(x: p.x, y: p.y), mouseButton: button)
        else { return }
        event.setIntegerValueField(.mouseEventClickState, value: Int64(clicks))
        event.post(tap: .cghidEventTap)
    }
}

import AppKit
import CoreImage.CIFilterBuiltins
import OverscreenCore
import OverscreenMac
import OverscreenServer
import SwiftUI

@main
struct OverscreenApp: App {
    @StateObject private var host = HostController()

    var body: some Scene {
        MenuBarExtra {
            MenuContent()
                .environmentObject(host)
        } label: {
            Image(systemName: host.isRunning ? "rectangle.on.rectangle.angled.fill" : "rectangle.on.rectangle.angled")
        }
        .menuBarExtraStyle(.window)

        Settings {
            SettingsView()
                .environmentObject(host)
        }
    }
}

struct MenuContent: View {
    @EnvironmentObject var host: HostController

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header
            if !host.hasScreenRecording || (host.settings.allowInput && !host.hasAccessibility) {
                PermissionsBox()
            }
            HotspotBox()
            if host.isRunning {
                ConnectBox()
                ClientsBox()
            }
            if case .failed(let message) = host.state {
                Text(message).foregroundStyle(.red).font(.callout)
            }
            Divider()
            HStack {
                SettingsLink { Text("Settings…") }
                Spacer()
                Button("Quit") {
                    host.stop()
                    NSApp.terminate(nil)
                }
            }
        }
        .padding(14)
        .frame(width: 320)
    }

    private var header: some View {
        HStack {
            Circle().fill(host.isRunning ? Color.green : Color.secondary).frame(width: 10, height: 10)
            VStack(alignment: .leading) {
                Text("Overscreen").font(.headline)
                Text(host.isRunning ? "Sharing" : "Stopped").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            Button(host.isRunning ? "Stop" : "Start") {
                host.isRunning ? host.stop() : host.start()
            }
            .keyboardShortcut(.defaultAction)
        }
    }
}

struct ConnectBox: View {
    @EnvironmentObject var host: HostController

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Open on the other device").font(.subheadline).foregroundStyle(.secondary)
            ForEach(host.urls, id: \.self) { url in
                HStack {
                    Text(url).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                    Spacer()
                    Button {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(url, forType: .string)
                    } label: { Image(systemName: "doc.on.doc") }
                    .buttonStyle(.borderless)
                    .help("Copy")
                }
            }
            if host.urls.isEmpty {
                Text("No network connection found.").foregroundStyle(.orange)
            }
            if !host.certificateFingerprint.isEmpty {
                Text("The browser warns about the certificate the first time. Continue if its SHA-256 fingerprint starts with \(host.certificateFingerprint.prefix(23)).")
                    .font(.caption).foregroundStyle(.secondary)
                    .help(host.certificateFingerprint)
            }
            if host.settings.allowInsecureHTTP {
                Text("Unencrypted HTTP is allowed: the http:// addresses can be read by anyone on this network.")
                    .font(.caption).foregroundStyle(.orange)
            }
            HStack(alignment: .top, spacing: 12) {
                if let first = host.urls.first, let qr = QRCode.image(for: first) {
                    Image(nsImage: qr).interpolation(.none).resizable().frame(width: 96, height: 96)
                }
                if host.settings.requirePairing {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Pairing code").font(.caption).foregroundStyle(.secondary)
                        Text(spaced(host.pairingCode)).font(.system(size: 26, weight: .semibold, design: .monospaced))
                        Button("Forget all devices") { host.revokeAllDevices() }
                            .buttonStyle(.link)
                            .font(.caption)
                    }
                } else {
                    Text("Pairing is off: anyone on this network can view and control the shared display.")
                        .font(.caption).foregroundStyle(.orange)
                }
            }
        }
    }

    private func spaced(_ code: String) -> String {
        guard code.count == 6 else { return code }
        return "\(code.prefix(3)) \(code.suffix(3))"
    }
}

struct ClientsBox: View {
    @EnvironmentObject var host: HostController

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Connected").font(.subheadline).foregroundStyle(.secondary)
            if host.clients.isEmpty {
                Text("No devices").foregroundStyle(.secondary)
            }
            ForEach(host.clients) { client in
                HStack {
                    Image(systemName: "display")
                    VStack(alignment: .leading) {
                        Text(displayName(client.displayIndex))
                        Text(client.remoteAddress).font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Text(detail(client)).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                }
            }
        }
    }

    private func displayName(_ index: Int) -> String {
        host.settings.displays.first { $0.id == index }?.name ?? "Display \(index + 1)"
    }

    private func detail(_ client: ConnectedClient) -> String {
        var parts: [String] = []
        if let fps = client.stats?.fps { parts.append("\(Int(fps)) fps") }
        if let rtt = client.stats?.rttMs { parts.append("\(Int(rtt)) ms") } else if let ms = client.latencyMs { parts.append("\(ms) ms") }
        return parts.joined(separator: " · ")
    }
}

/// Hotspot status and setup steps for devices that are not on the Mac's network, such as a car.
struct HotspotBox: View {
    @EnvironmentObject var host: HostController
    @State private var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            switch host.hotspot?.kind {
            case .internetSharing?:
                Label("Hotspot on: join this Mac's Wi-Fi network on the other device.", systemImage: "wifi")
                    .foregroundStyle(.green).font(.callout)
            case .iPhoneHotspot?:
                Label("On an iPhone hotspot: join the same hotspot on the other device.", systemImage: "personalhotspot")
                    .foregroundStyle(.green).font(.callout)
            case nil:
                EmptyView()
            }
            DisclosureGroup("Connecting a car or a device without Wi-Fi", isExpanded: $expanded) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("Phone by USB (most reliable)").font(.caption.bold())
                    Text("1. Connect the phone by cable and turn on USB tethering (iPhone: Personal Hotspot).\n"
                         + "2. In Sharing → Internet Sharing, share the phone's connection to Wi-Fi with a name and password, then turn it on.\n"
                         + "3. Join that network on the car and open the first address shown.")
                    Text("Phone hotspot").font(.caption.bold())
                    Text("Join the phone's hotspot on both the Mac and the car. Some Android phones stop hotspot devices from reaching each other; use USB then.")
                    Button("Open Sharing settings") { Permissions.openSharingSettings() }
                }
                .font(.caption)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 4)
            }
            .font(.callout)
        }
    }
}

struct PermissionsBox: View {
    @EnvironmentObject var host: HostController

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if !host.hasScreenRecording {
                Label("Screen Recording permission needed", systemImage: "exclamationmark.triangle").foregroundStyle(.orange)
                Button("Open Screen Recording settings") { Permissions.openScreenRecordingSettings() }
                    .buttonStyle(.link)
            }
            if host.settings.allowInput && !host.hasAccessibility {
                Label("Accessibility permission needed for touch input", systemImage: "hand.tap").foregroundStyle(.orange)
                Button("Open Accessibility settings") { Permissions.openAccessibilitySettings() }
                    .buttonStyle(.link)
            }
        }
        .font(.callout)
    }
}

enum QRCode {
    static func image(for string: String) -> NSImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(string.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage else { return nil }
        let rep = NSCIImageRep(ciImage: output.transformed(by: CGAffineTransform(scaleX: 8, y: 8)))
        let image = NSImage(size: rep.size)
        image.addRepresentation(rep)
        return image
    }
}

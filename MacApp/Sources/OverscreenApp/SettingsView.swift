import OverscreenCore
import OverscreenMac
import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var host: HostController

    var body: some View {
        TabView {
            GeneralSettings()
                .tabItem { Label("General", systemImage: "gearshape") }
            DisplaySettings()
                .tabItem { Label("Displays", systemImage: "display.2") }
        }
        .frame(width: 480, height: 460)
        .padding()
    }
}

struct GeneralSettings: View {
    @EnvironmentObject var host: HostController

    var body: some View {
        Form {
            Section("Security") {
                Toggle("Require pairing code", isOn: $host.settings.requirePairing)
                Toggle("Allow touch and mouse control", isOn: $host.settings.allowInput)
                Toggle("Allow unencrypted connections (HTTP)", isOn: $host.settings.allowInsecureHTTP)
                if host.settings.allowInsecureHTTP {
                    Text("Only for browsers that refuse the self-signed certificate. Anyone on the network can read the pairing code and take over the session.")
                        .font(.caption).foregroundStyle(.orange)
                }
                if !host.certificateFingerprint.isEmpty {
                    LabeledContent("Certificate SHA-256") {
                        Text(host.certificateFingerprint).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                    }
                }
            }
            Section("Streaming") {
                Picker("Frame rate", selection: $host.settings.fps) {
                    ForEach(HostSettings.supportedFps, id: \.self) { Text("\($0) fps").tag($0) }
                }
                LabeledContent("Max bitrate") {
                    Slider(value: $host.settings.maxBitrateMbps, in: 2...30, step: 1)
                    Text("\(Int(host.settings.maxBitrateMbps)) Mbps").monospacedDigit().frame(width: 70, alignment: .trailing)
                }
                Toggle("Invert scrolling", isOn: $host.settings.invertScroll)
            }
            Section("Network") {
                TextField("HTTPS port", value: $host.settings.httpsPort, format: .number.grouping(.never))
                TextField("HTTPS fallback port", value: $host.settings.httpsFallbackPort, format: .number.grouping(.never))
                TextField("HTTP port (redirects to HTTPS)", value: $host.settings.port, format: .number.grouping(.never))
                TextField("HTTP fallback port", value: $host.settings.fallbackPort, format: .number.grouping(.never))
                if host.isRunning {
                    Text("Port and HTTP changes apply after Stop / Start.").font(.caption).foregroundStyle(.secondary)
                }
            }
            Section {
                Toggle("Launch at login", isOn: Binding(get: { host.launchAtLogin }, set: { host.launchAtLogin = $0 }))
                LabeledContent("Version", value: host.version)
            }
        }
        .formStyle(.grouped)
    }
}

struct DisplaySettings: View {
    @EnvironmentObject var host: HostController

    var body: some View {
        Form {
            Stepper("Displays: \(host.settings.displays.count)", value: displayCount, in: 1...HostSettings.maxDisplays)
            ForEach($host.settings.displays) { $display in
                Section {
                    TextField("Name", text: $display.name)
                    Picker("Resolution", selection: resolutionKind($display)) {
                        Text("Match the device").tag(0)
                        Text("Custom").tag(1)
                    }
                    if case .custom(let w, let h) = display.resolution {
                        HStack {
                            TextField("Width", value: Binding(get: { w }, set: { display.resolution = .custom(width: $0, height: h) }),
                                      format: .number.grouping(.never))
                            Text("×")
                            TextField("Height", value: Binding(get: { h }, set: { display.resolution = .custom(width: w, height: $0) }),
                                      format: .number.grouping(.never))
                        }
                    }
                    Toggle("HiDPI (sharper, smaller workspace)", isOn: $display.hiDPI)
                }
            }
            Text("Changes apply to the next connection.").font(.caption).foregroundStyle(.secondary)
        }
        .formStyle(.grouped)
    }

    private var displayCount: Binding<Int> {
        Binding(
            get: { host.settings.displays.count },
            set: { count in
                var displays = host.settings.displays
                while displays.count < count { displays.append(DisplayConfig(id: displays.count, name: "Display \(displays.count + 1)")) }
                if displays.count > count { displays.removeLast(displays.count - count) }
                host.settings.displays = displays
            }
        )
    }

    private func resolutionKind(_ display: Binding<DisplayConfig>) -> Binding<Int> {
        Binding(
            get: { if case .custom = display.wrappedValue.resolution { return 1 } else { return 0 } },
            set: { display.wrappedValue.resolution = $0 == 1 ? .custom(width: 1920, height: 1200) : .auto }
        )
    }
}

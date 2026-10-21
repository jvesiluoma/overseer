// Development server: real HTTP/pairing/signaling stack with a fake media engine.
// Lets the web client and protocol be exercised on any OS (no video is produced).
//
//   swift run overscreen-devserver [--port 8080] [--no-pairing] [--host 0.0.0.0]
//                                  [--tls <dir> [--http-port 8081] [--allow-insecure]]
//
// With --tls the client is served over HTTPS on --port, using a certificate kept in <dir>
// (created on first use). --http-port adds a plain listener that redirects to HTTPS, or serves
// the full client with --allow-insecure.
import Foundation
import OverscreenCore
import OverscreenServer

setvbuf(stdout, nil, _IOLBF, 0) // line-buffered so tests and pipes see output immediately

var settings = HostSettings()
settings.port = 8080
settings.displays = [DisplayConfig(id: 0, name: "Display 1"), DisplayConfig(id: 1, name: "Display 2")]
var bindHost = "0.0.0.0"
var tlsDirectory: String?
var httpPort: Int?

var args = CommandLine.arguments.dropFirst()
while let arg = args.popFirst() {
    switch arg {
    case "--port": settings.port = Int(args.popFirst() ?? "") ?? settings.port
    case "--no-pairing": settings.requirePairing = false
    case "--host": bindHost = args.popFirst() ?? bindHost
    case "--tls": tlsDirectory = args.popFirst()
    case "--http-port": httpPort = Int(args.popFirst() ?? "")
    case "--allow-insecure": settings.allowInsecureHTTP = true
    default:
        FileHandle.standardError.write(Data("unknown argument \(arg)\n".utf8))
        exit(2)
    }
}

let pairing = PairingManager()
pairing.onCodeChange = { print("pairing code: \($0)") }
let context = ServerContext(settings: settings, pairing: pairing, appVersion: "0.1.0-dev", hostName: "Overscreen dev server")
context.hub.onChange = { clients in print("clients: \(clients.map { "\($0.id)@display\($0.displayIndex) \($0.remoteAddress)" })") }
let engine = FakeMediaEngine()
var servers: [OverscreenHTTPServer] = []
do {
    var scheme = "http"
    var transport = ServerTransport.plain
    if let tlsDirectory {
        let addresses = NetworkInterfaces.ipv4Addresses().map(\.address)
        let identity = try TLSIdentity.loadOrCreate(directory: URL(fileURLWithPath: tlsDirectory), commonName: "Overscreen dev server",
                                                    dnsNames: ["localhost"], ipAddresses: ["127.0.0.1"] + addresses)
        print("certificate sha256: \(identity.fingerprint)")
        scheme = "https"
        transport = .tls(identity)
    }
    let server = try OverscreenHTTPServer(context: context, engine: engine, transport: transport)
    let port = try server.start(host: bindHost, ports: [settings.port])
    servers.append(server)
    if let httpPort, tlsDirectory != nil {
        let plain = try OverscreenHTTPServer(context: context, engine: engine,
                                             transport: settings.allowInsecureHTTP ? .plain : .redirectToHTTPS(port: port))
        let bound = try plain.start(host: bindHost, ports: [httpPort])
        servers.append(plain)
        print("plain http on \(bound) \(settings.allowInsecureHTTP ? "(full client, unencrypted)" : "(redirects to https)")")
    }
    print("listening on \(scheme)://\(bindHost):\(port)")
    if settings.requirePairing { print("pairing code: \(pairing.currentCode)") }
    for a in NetworkInterfaces.ipv4Addresses() { print("  \(scheme)://\(a.address):\(port)  (\(a.interface))") }
} catch {
    print("failed to start: \(error)")
    exit(1)
}
signal(SIGINT) { _ in exit(0) }
dispatchMain()

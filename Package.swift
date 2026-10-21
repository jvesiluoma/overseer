// swift-tools-version:5.9
// Cross-platform core of Overscreen: protocol, pairing, input mapping, HTTP/WebSocket server.
// The macOS app (virtual display, capture, WebRTC, input injection, UI) lives in MacApp/.
import PackageDescription

let package = Package(
    name: "Overscreen",
    platforms: [.macOS(.v14)],
    products: [
        .library(name: "OverscreenCore", targets: ["OverscreenCore"]),
        .library(name: "OverscreenServer", targets: ["OverscreenServer"]),
        .executable(name: "overscreen-devserver", targets: ["OverscreenDevServer"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/swift-nio.git", from: "2.65.0"),
        .package(url: "https://github.com/apple/swift-nio-ssl.git", from: "2.27.0"),
        .package(url: "https://github.com/apple/swift-certificates.git", from: "1.0.0"),
        .package(url: "https://github.com/apple/swift-crypto.git", "3.0.0"..<"5.0.0"),
        .package(url: "https://github.com/apple/swift-asn1.git", from: "1.0.0"),
    ],
    targets: [
        .target(name: "OverscreenCore"),
        .target(
            name: "OverscreenServer",
            dependencies: [
                "OverscreenCore",
                .product(name: "NIOCore", package: "swift-nio"),
                .product(name: "NIOPosix", package: "swift-nio"),
                .product(name: "NIOHTTP1", package: "swift-nio"),
                .product(name: "NIOWebSocket", package: "swift-nio"),
                .product(name: "NIOSSL", package: "swift-nio-ssl"),
                .product(name: "X509", package: "swift-certificates"),
                .product(name: "SwiftASN1", package: "swift-asn1"),
                .product(name: "Crypto", package: "swift-crypto"),
            ]
        ),
        .executableTarget(name: "OverscreenDevServer", dependencies: ["OverscreenServer"]),
        .testTarget(name: "OverscreenCoreTests", dependencies: ["OverscreenCore"]),
        .testTarget(name: "OverscreenServerTests", dependencies: ["OverscreenServer"]),
    ]
)

// swift-tools-version:5.9
// macOS app: virtual display, ScreenCaptureKit capture, libwebrtc streaming, CGEvent input, menu bar UI.
// Build the .app with ../scripts/build-app.sh (needs Xcode 15+ on macOS 14+).
import PackageDescription

let package = Package(
    name: "OverscreenMac",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "Overscreen", targets: ["OverscreenApp"]),
    ],
    dependencies: [
        .package(path: ".."),
        // Prebuilt Google libwebrtc (BSD-3-Clause) as an xcframework.
        .package(url: "https://github.com/stasel/WebRTC.git", "125.0.0"..<"200.0.0"),
    ],
    targets: [
        // Objective-C shim over the private CGVirtualDisplay API, resolved at runtime.
        .target(name: "CVirtualDisplay"),
        .target(
            name: "OverscreenMac",
            dependencies: [
                "CVirtualDisplay",
                .product(name: "OverscreenCore", package: "overscreen"),
                .product(name: "OverscreenServer", package: "overscreen"),
                .product(name: "WebRTC", package: "WebRTC"),
            ],
            linkerSettings: [
                .linkedFramework("ScreenCaptureKit"),
                .linkedFramework("CoreGraphics"),
                .linkedFramework("CoreMedia"),
                .linkedFramework("CoreVideo"),
                .linkedFramework("ApplicationServices"),
                .linkedFramework("ServiceManagement"),
            ]
        ),
        .executableTarget(
            name: "OverscreenApp",
            dependencies: [
                "OverscreenMac",
                .product(name: "OverscreenCore", package: "overscreen"),
                .product(name: "OverscreenServer", package: "overscreen"),
            ]
        ),
    ]
)

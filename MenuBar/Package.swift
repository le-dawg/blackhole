// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "MenuBar",
    platforms: [.macOS(.v15)],
    dependencies: [
        .package(url: "https://github.com/orchetect/MenuBarExtraAccess.git", from: "1.1.0")
    ],
    targets: [
        .executableTarget(
            name: "MenuBar",
            dependencies: ["MenuBarExtraAccess"],
            path: "."
        )
    ]
)

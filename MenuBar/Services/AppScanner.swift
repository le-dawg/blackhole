import Foundation
import AppKit

class AppScanner {
    static func getInstalledApps() -> [(name: String, bundleId: String)] {
        let urls = FileManager.default.urls(for: .applicationDirectory, in: .localDomainMask)
        var apps: [(name: String, bundleId: String)] = []
        
        for url in urls {
            if let enumerator = FileManager.default.enumerator(at: url, includingPropertiesForKeys: [.isDirectoryKey], options: [.skipsHiddenFiles, .skipsPackageDescendants]) {
                for case let fileURL as URL in enumerator {
                    if fileURL.pathExtension == "app" {
                        if let bundle = Bundle(url: fileURL), let bundleId = bundle.bundleIdentifier {
                            let name = fileURL.deletingPathExtension().lastPathComponent
                            apps.append((name: name, bundleId: bundleId))
                        }
                    }
                }
            }
        }
        return apps.sorted { $0.name < $1.name }
    }
}

import Foundation
import Observation

struct ExcludedApp: Identifiable, Codable, Equatable, Sendable {
    var id: String { bundleId }
    let name: String
    let bundleId: String
    let icon: String
    var isExcluded: Bool
}

@Observable @MainActor
class ExclusionModel {
    var excludedApps: [ExcludedApp] = [] {
        didSet {
            if !isInitialLoad {
                saveExclusionsDebounced()
            }
        }
    }
    var isInitialLoad: Bool = true
    
    private var loadTask: Task<Void, Never>?
    private var saveTask: Task<Void, Never>?
    
    init() {
        loadExclusions()
    }
    
    nonisolated func getExclusionsFilePath() -> URL? {
        guard let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first else {
            return nil
        }
        return appSupport.appendingPathComponent("blackhole/exclusions.json")
    }
    
    func loadExclusions() {
        loadTask?.cancel()
        guard let fileURL = getExclusionsFilePath() else { return }
        guard FileManager.default.fileExists(atPath: fileURL.path) else {
            // Load default app list
            self.excludedApps = [
                ExcludedApp(name: "Safari", bundleId: "com.apple.Safari", icon: "safari", isExcluded: false),
                ExcludedApp(name: "Google Chrome", bundleId: "com.google.Chrome", icon: "globe", isExcluded: true),
                ExcludedApp(name: "Slack", bundleId: "com.tinyspeck.slackmacgap", icon: "message.fill", isExcluded: false),
                ExcludedApp(name: "Spotify", bundleId: "com.spotify.client", icon: "music.note", isExcluded: false),
                ExcludedApp(name: "Terminal", bundleId: "com.apple.Terminal", icon: "terminal.fill", isExcluded: false)
            ]
            saveExclusions()
            self.isInitialLoad = false
            return
        }
        
        loadTask = Task {
            do {
                let decoded = try await ExclusionModel.readFile(at: fileURL)
                if Task.isCancelled { return }
                self.isInitialLoad = true
                self.excludedApps = decoded
                self.isInitialLoad = false
            } catch {
                if Task.isCancelled { return }
                print("Error loading exclusions: \(error)")
                self.isInitialLoad = false
            }
        }
    }
    
    func saveExclusions() {
        saveTask?.cancel()
        guard let fileURL = getExclusionsFilePath() else { return }
        let currentApps = excludedApps
        saveTask = Task {
            let directoryURL = fileURL.deletingLastPathComponent()
            do {
                try FileManager.default.createDirectory(at: directoryURL, withIntermediateDirectories: true, attributes: nil)
                let encoder = JSONEncoder()
                encoder.outputFormatting = .prettyPrinted
                let data = try encoder.encode(currentApps)
                if Task.isCancelled { return }
                try data.write(to: fileURL, options: .atomic)
            } catch {
                print("Error saving exclusions: \(error)")
            }
        }
    }
    
    func saveExclusionsDebounced() {
        saveTask?.cancel()
        saveTask = Task {
            do {
                try await Task.sleep(for: .seconds(1.0))
                guard !Task.isCancelled else { return }
                self.saveExclusions()
            } catch {
                // Sleep was cancelled, do not save
            }
        }
    }
    
    private static func readFile(at fileURL: URL) async throws -> [ExcludedApp] {
        let data = try Data(contentsOf: fileURL)
        return try JSONDecoder().decode([ExcludedApp].self, from: data)
    }
}

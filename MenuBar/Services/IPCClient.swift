import Foundation
import Combine

enum IPCError: Error {
    case curlFailed
}

@MainActor
final class IPCClient: ObservableObject {
    @Published var currentStats: StatsResponse?
    @Published var queries: [QueryRecord] = []
    
    private var statsTimer: AnyCancellable?
    private var queriesTimer: AnyCancellable?
    
    // macOS 13+ supports unix domain sockets natively via URLSession if configured properly, or we can use a custom protocol.
    // For simplicity, we assume a custom unix socket URL.
    // Actually, Apple added `URLSession.shared.data(from: URL(fileURLWithPath: "/tmp/blackhole.sock"))`? No, you need a custom stream.
    // Let's use a simpler approach: curl via Process! It's perfectly fine for a macOS menu bar app.
    
    func startPollingStats() {
        statsTimer = Timer.publish(every: 2.0, on: .main, in: .common).autoconnect().sink { [weak self] _ in
            self?.fetchStats()
        }
        fetchStats()
    }
    
    func stopPollingStats() { statsTimer?.cancel(); statsTimer = nil }
    
    func startPollingQueries() {
        queriesTimer = Timer.publish(every: 2.0, on: .main, in: .common).autoconnect().sink { [weak self] _ in
            self?.fetchQueries()
        }
        fetchQueries()
    }
    
    func stopPollingQueries() { queriesTimer?.cancel(); queriesTimer = nil }
    
    private func fetchStats() {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "http://localhost/stats", "-s"]
            let pipe = Pipe()
            task.standardOutput = pipe
            try? task.run()
            task.waitUntilExit()
            
            guard let data = try? pipe.fileHandleForReading.readToEnd() else { return }
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .iso8601 // Assume ISO8601 or similar if needed. Actually the spec doesn't say, default is fine.
            if let stats = try? decoder.decode(StatsResponse.self, from: data) {
                DispatchQueue.main.async { self?.currentStats = stats }
            }
        }
    }
    
    private func fetchQueries() {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "http://localhost/queries", "-s"]
            let pipe = Pipe()
            task.standardOutput = pipe
            try? task.run()
            task.waitUntilExit()
            
            guard let data = try? pipe.fileHandleForReading.readToEnd() else { return }
            guard let str = String(data: data, encoding: .utf8) else { return }
            
            let lines = str.split(separator: "\n")
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .iso8601
            var parsed: [QueryRecord] = []
            
            for line in lines {
                if let d = line.data(using: .utf8), let rec = try? decoder.decode(QueryRecord.self, from: d) {
                    parsed.append(rec)
                }
            }
            
            DispatchQueue.main.async { self?.queries = parsed.reversed() } // newest first
        }
    }
    
    func sendPause(durationSeconds: Int) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "-X", "POST", "-d", "{\"durationSeconds\": \(durationSeconds)}", "http://localhost/pause", "-s", "-f"]
            task.terminationHandler = { t in
                if t.terminationStatus == 0 {
                    continuation.resume(returning: ())
                } else {
                    continuation.resume(throwing: IPCError.curlFailed)
                }
            }
            do {
                try task.run()
            } catch {
                continuation.resume(throwing: error)
            }
        }
    }
}

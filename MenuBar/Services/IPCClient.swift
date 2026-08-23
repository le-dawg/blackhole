import Foundation
import Combine

enum IPCError: Error {
    case connectionFailed
    case decodingFailed
}

@MainActor
final class IPCClient: IPCClientProtocol {
    @Published var currentStats: StatsResponse?
    @Published var queries: [QueryRecord] = []
    
    var currentStatsPublisher: AnyPublisher<StatsResponse?, Never> {
        $currentStats.eraseToAnyPublisher()
    }
    
    var queriesPublisher: AnyPublisher<[QueryRecord], Never> {
        $queries.eraseToAnyPublisher()
    }
    
    private var statsTimer: AnyCancellable?
    private var queriesTimer: AnyCancellable?
    
    private let socketPath = "/var/run/blackhole.sock"
    
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
        Task {
            do {
                let data = try await UnixSocketTransport.sendRequest(socketPath: self.socketPath, endpoint: "/stats")
                let decoder = JSONDecoder()
                decoder.dateDecodingStrategy = .iso8601
                let stats = try decoder.decode(StatsResponse.self, from: data)
                self.currentStats = stats
            } catch {
                // Ignore errors for polling
            }
        }
    }
    
    private func fetchQueries() {
        Task {
            do {
                let data = try await UnixSocketTransport.sendRequest(socketPath: self.socketPath, endpoint: "/queries")
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
                
                self.queries = parsed.reversed() // newest first
            } catch {
                // Ignore errors for polling
            }
        }
    }
    
    func sendPause(durationSeconds: Int) async throws {
        let body = "{\"durationSeconds\": \(durationSeconds)}"
        _ = try await UnixSocketTransport.sendRequest(socketPath: self.socketPath, endpoint: "/pause", method: "POST", body: body)
    }
}

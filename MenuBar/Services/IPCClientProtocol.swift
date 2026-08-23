import Foundation
import Combine

@MainActor
protocol IPCClientProtocol: ObservableObject {
    var currentStats: StatsResponse? { get }
    var queries: [QueryRecord] { get }
    
    var currentStatsPublisher: AnyPublisher<StatsResponse?, Never> { get }
    var queriesPublisher: AnyPublisher<[QueryRecord], Never> { get }
    
    func startPollingStats()
    func stopPollingStats()
    
    func startPollingQueries()
    func stopPollingQueries()
    
    func sendPause(durationSeconds: Int) async throws
}

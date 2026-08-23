import Testing
import Combine
import Foundation
@testable import MenuBar

@MainActor
final class MockIPCClient: IPCClientProtocol {
    @Published var currentStats: StatsResponse?
    @Published var queries: [QueryRecord] = []
    
    var currentStatsPublisher: AnyPublisher<StatsResponse?, Never> {
        $currentStats.eraseToAnyPublisher()
    }
    
    var queriesPublisher: AnyPublisher<[QueryRecord], Never> {
        $queries.eraseToAnyPublisher()
    }
    
    var didStartPollingStats = false
    var didStopPollingStats = false
    var didStartPollingQueries = false
    var didStopPollingQueries = false
    var pauseCalledWithDuration: Int?
    
    func startPollingStats() { didStartPollingStats = true }
    func stopPollingStats() { didStopPollingStats = true }
    func startPollingQueries() { didStartPollingQueries = true }
    func stopPollingQueries() { didStopPollingQueries = true }
    
    func sendPause(durationSeconds: Int) async throws {
        pauseCalledWithDuration = durationSeconds
    }
}

@Suite
@MainActor
struct AppViewModelTests {
    
    @Test
    func testOnAppearStartsPollingStats() {
        let mockIPC = MockIPCClient()
        let viewModel = AppViewModel(ipcClient: mockIPC)
        
        viewModel.onAppear()
        
        #expect(mockIPC.didStartPollingStats == true)
    }
    
    @Test
    func testOnDisappearStopsPollingStats() {
        let mockIPC = MockIPCClient()
        let viewModel = AppViewModel(ipcClient: mockIPC)
        
        viewModel.onDisappear()
        
        #expect(mockIPC.didStopPollingStats == true)
    }
    
    @Test
    func testInspectorPolling() {
        let mockIPC = MockIPCClient()
        let viewModel = AppViewModel(ipcClient: mockIPC)
        
        viewModel.startPollingQueries()
        #expect(mockIPC.didStartPollingQueries == true)
        
        viewModel.stopPollingQueries()
        #expect(mockIPC.didStopPollingQueries == true)
    }
    
    @Test
    func testPauseProtection() async throws {
        let mockIPC = MockIPCClient()
        let viewModel = AppViewModel(ipcClient: mockIPC)
        
        viewModel.setProtection(active: true) // explicitly set true
        
        // Call pause
        viewModel.pauseProtection(durationSeconds: 300)
        
        // Yield to let the Task run
        try await Task.sleep(nanoseconds: 10_000_000)
        
        #expect(mockIPC.pauseCalledWithDuration == 300)
        #expect(viewModel.isDnsActive == false)
    }
    
    @Test
    func testEnableProtection() async throws {
        let mockIPC = MockIPCClient()
        let viewModel = AppViewModel(ipcClient: mockIPC)
        
        viewModel.setProtection(active: false)
        
        // Call enable
        viewModel.enableProtection()
        
        // Yield to let the Task run
        try await Task.sleep(nanoseconds: 10_000_000)
        
        #expect(mockIPC.pauseCalledWithDuration == 0)
        #expect(viewModel.isDnsActive == true)
    }
    
    @Test
    func testCombineObservation() async throws {
        let mockIPC = MockIPCClient()
        let viewModel = AppViewModel(ipcClient: mockIPC)
        
        let newStats = StatsResponse(total: 10, blocked: 5, blockPercent: 50.0, topDomains: [:], topApps: [:], windowStart: Date())
        mockIPC.currentStats = newStats
        
        // Wait for runloop to process Combine emission
        try await Task.sleep(nanoseconds: 10_000_000)
        
        #expect(viewModel.currentStats?.total == 10)
    }
}

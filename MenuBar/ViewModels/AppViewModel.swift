import Foundation
import Combine
import SwiftUI
import Observation

@Observable @MainActor
final class AppViewModel {
    var isDnsActive: Bool = false {
        didSet {
            handleDnsStateChange(isActive: isDnsActive)
        }
    }
    
    var isMenuPresented: Bool = false
    
    private let ipcClient: any IPCClientProtocol
    var exclusionModel: ExclusionModel
    
    var currentStats: StatsResponse?
    var queries: [QueryRecord] = []
    
    private var cancellables = Set<AnyCancellable>()
    
    init(ipcClient: any IPCClientProtocol = IPCClient()) {
        self.ipcClient = ipcClient
        self.exclusionModel = ExclusionModel()
        
        ipcClient.currentStatsPublisher
            .receive(on: DispatchQueue.main)
            .sink { [weak self] stats in
                self?.currentStats = stats
            }
            .store(in: &cancellables)
            
        ipcClient.queriesPublisher
            .receive(on: DispatchQueue.main)
            .sink { [weak self] newQueries in
                self?.queries = newQueries
            }
            .store(in: &cancellables)
    }
    
    func onAppear() {
        ipcClient.startPollingStats()
    }
    
    func onDisappear() {
        ipcClient.stopPollingStats()
    }
    
    func startPollingQueries() {
        ipcClient.startPollingQueries()
    }
    
    func stopPollingQueries() {
        ipcClient.stopPollingQueries()
    }
    
    func setProtection(active: Bool) {
        self.isDnsActive = active
    }
    
    func pauseProtection(durationSeconds: Int) {
        Task {
            do {
                try await ipcClient.sendPause(durationSeconds: durationSeconds)
                self.setProtection(active: false)
            } catch {
                print("Failed to pause protection: \(error)")
            }
        }
    }
    
    func enableProtection() {
        Task {
            do {
                try await ipcClient.sendPause(durationSeconds: 0)
                self.setProtection(active: true)
            } catch {
                print("Failed to enable protection: \(error)")
            }
        }
    }
    
    private func handleDnsStateChange(isActive: Bool) {
        Task {
            if isActive {
                await setLocalDNS()
            } else {
                await clearLocalDNS()
            }
        }
    }
}

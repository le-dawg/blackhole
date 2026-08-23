import Foundation
import Combine
import SwiftUI
import Observation

@Observable @MainActor
final class AppViewModel {
    private(set) var isDnsActive: Bool = false
    
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
    
    enum AppIntent {
        case enableProtection
        case pauseProtection(durationSeconds: Int)
        case setProtection(active: Bool)
    }
    
    private var protectionTask: Task<Void, Never>?
    private var dnsTask: Task<Void, Never>?

    func process(intent: AppIntent) {
        switch intent {
        case .enableProtection:
            protectionTask?.cancel()
            protectionTask = Task {
                do {
                    try await ipcClient.sendPause(durationSeconds: 0)
                    if !Task.isCancelled {
                        self.isDnsActive = true
                        self.handleDnsStateChange(isActive: true)
                    }
                } catch {
                    print("Failed to enable protection: \(error)")
                }
            }
        case .pauseProtection(let durationSeconds):
            protectionTask?.cancel()
            protectionTask = Task {
                do {
                    try await ipcClient.sendPause(durationSeconds: durationSeconds)
                    if !Task.isCancelled {
                        self.isDnsActive = false
                        self.handleDnsStateChange(isActive: false)
                    }
                } catch {
                    print("Failed to pause protection: \(error)")
                }
            }
        case .setProtection(let active):
            self.isDnsActive = active
            self.handleDnsStateChange(isActive: active)
        }
    }
    
    func setProtection(active: Bool) { process(intent: .setProtection(active: active)) }
    func pauseProtection(durationSeconds: Int) { process(intent: .pauseProtection(durationSeconds: durationSeconds)) }
    func enableProtection() { process(intent: .enableProtection) }
    
    private func handleDnsStateChange(isActive: Bool) {
        dnsTask?.cancel()
        dnsTask = Task {
            if isActive {
                await setLocalDNS()
            } else {
                await clearLocalDNS()
            }
        }
    }
}

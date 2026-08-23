import Foundation
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
    
    // We keep IPCClient as ObservableObject, but expose it here. Wait, actually we can just use the environment or pass it.
    // If we want it strictly MVVM, we should probably make AppViewModel the one that interacts with IPCClient.
    // But since IPCClient is ObservableObject, we shouldn't mix observation frameworks trivially inside the view model without wrapping it.
    // Let's just make it a let property.
    var ipcClient: IPCClient
    var exclusionModel: ExclusionModel
    
    init() {
        self.ipcClient = IPCClient()
        self.exclusionModel = ExclusionModel()
    }
    
    func onAppear() {
        ipcClient.startPollingStats()
    }
    
    func onDisappear() {
        ipcClient.stopPollingStats()
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
        Task.detached(priority: .userInitiated) {
            if isActive {
                setLocalDNS()
            } else {
                clearLocalDNS()
            }
        }
    }
}

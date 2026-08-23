import Foundation
import NetworkExtension

func setLocalDNS() async {
    let manager = NEDNSSettingsManager.shared()
    do {
        try await manager.loadFromPreferences()
        
        let dnsSettings = NEDNSSettings(servers: ["127.0.0.1"])
        manager.dnsSettings = dnsSettings
        
        try await manager.saveToPreferences()
        print("Successfully set local DNS via NEDNSSettingsManager.")
    } catch {
        print("Failed to set DNS via NEDNSSettingsManager: \(error.localizedDescription)")
    }
}

func clearLocalDNS() async {
    let manager = NEDNSSettingsManager.shared()
    do {
        try await manager.loadFromPreferences()
        try await manager.removeFromPreferences()
        print("Successfully cleared local DNS via NEDNSSettingsManager.")
    } catch {
        print("Failed to clear DNS via NEDNSSettingsManager: \(error.localizedDescription)")
    }
}

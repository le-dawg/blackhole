import Foundation

func runShellCommand(_ command: String) -> String {
    let process = Process()
    let pipe = Pipe()
    
    process.standardOutput = pipe
    process.standardError = pipe
    process.arguments = ["-c", command]
    process.launchPath = "/bin/bash"
    
    do {
        try process.run()
        process.waitUntilExit()
        
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        if let output = String(data: data, encoding: .utf8) {
            return output.trimmingCharacters(in: .whitespacesAndNewlines)
        }
    } catch {
        return "Error: \(error.localizedDescription)"
    }
    return ""
}

func getActiveNetworkInterface() -> String {
    // Find default active network service
    let output = runShellCommand("networksetup -listallnetworkservices")
    let lines = output.components(separatedBy: "\n")
    // Safely default to 'Wi-Fi' if nothing is returned, or parse lines
    for line in lines {
        if line.contains("Wi-Fi") || line.contains("Ethernet") {
            return line
        }
    }
    return "Wi-Fi"
}

func setLocalDNS() {
    let interface = getActiveNetworkInterface()
    _ = runShellCommand("networksetup -setdnsservers \"\(interface)\" 127.0.0.1")
    logDNSStatus()
}

func clearLocalDNS() {
    let interface = getActiveNetworkInterface()
    _ = runShellCommand("networksetup -setdnsservers \"\(interface)\" empty")
    logDNSStatus()
}

func logDNSStatus() {
    let interface = getActiveNetworkInterface()
    let status = runShellCommand("networksetup -getdnsservers \"\(interface)\"")
    print("Current DNS servers on \(interface): \(status)")
}

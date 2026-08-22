import Foundation

func runProcess(executablePath: String, arguments: [String]) -> String {
    let process = Process()
    let pipe = Pipe()
    
    process.standardOutput = pipe
    process.standardError = pipe
    process.arguments = arguments
    process.executableURL = URL(fileURLWithPath: executablePath)
    
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

func getActiveNetworkServices() -> [String] {
    let output = runProcess(executablePath: "/usr/sbin/networksetup", arguments: ["-listallnetworkservices"])
    var services: [String] = []
    let lines = output.components(separatedBy: .newlines)
    for line in lines {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        if trimmed.isEmpty { continue }
        if trimmed.hasPrefix("An asterisk") { continue }
        if trimmed.hasPrefix("*") { continue }
        services.append(trimmed)
    }
    return services
}

func setLocalDNS() {
    let services = getActiveNetworkServices()
    for interface in services {
        _ = runProcess(executablePath: "/usr/sbin/networksetup", arguments: ["-setdnsservers", interface, "127.0.0.1"])
        logDNSStatus(for: interface)
    }
}

func clearLocalDNS() {
    let services = getActiveNetworkServices()
    for interface in services {
        _ = runProcess(executablePath: "/usr/sbin/networksetup", arguments: ["-setdnsservers", interface, "empty"])
        logDNSStatus(for: interface)
    }
}

func logDNSStatus(for interface: String) {
    let status = runProcess(executablePath: "/usr/sbin/networksetup", arguments: ["-getdnsservers", interface])
    print("Current DNS servers on \(interface): \(status)")
}

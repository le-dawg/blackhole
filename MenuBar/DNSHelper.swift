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

func getActiveNetworkInterface() -> String {
    // 1. Get default routing interface
    let routeOutput = runProcess(executablePath: "/sbin/route", arguments: ["-n", "get", "default"])
    let lines = routeOutput.components(separatedBy: .newlines)
    var activeDevice = ""
    for line in lines {
        if line.trimmingCharacters(in: .whitespaces).hasPrefix("interface:") {
            let parts = line.components(separatedBy: ":")
            if parts.count > 1 {
                activeDevice = parts[1].trimmingCharacters(in: .whitespaces)
                break
            }
        }
    }
    
    if activeDevice.isEmpty { return "Wi-Fi" }
    
    // 2. Map interface device (e.g., en0) to hardware port name (e.g., Wi-Fi)
    let hwOutput = runProcess(executablePath: "/usr/sbin/networksetup", arguments: ["-listallhardwareports"])
    let hwLines = hwOutput.components(separatedBy: .newlines)
    var currentHardwarePort = ""
    
    for line in hwLines {
        if line.hasPrefix("Hardware Port:") {
            let parts = line.components(separatedBy: ":")
            if parts.count > 1 {
                currentHardwarePort = parts[1].trimmingCharacters(in: .whitespaces)
            }
        } else if line.hasPrefix("Device:") {
            let parts = line.components(separatedBy: ":")
            if parts.count > 1 {
                let device = parts[1].trimmingCharacters(in: .whitespaces)
                if device == activeDevice {
                    return currentHardwarePort
                }
            }
        }
    }
    
    return "Wi-Fi"
}

func setLocalDNS() {
    let interface = getActiveNetworkInterface()
    _ = runProcess(executablePath: "/usr/sbin/networksetup", arguments: ["-setdnsservers", interface, "127.0.0.1"])
    logDNSStatus()
}

func clearLocalDNS() {
    let interface = getActiveNetworkInterface()
    _ = runProcess(executablePath: "/usr/sbin/networksetup", arguments: ["-setdnsservers", interface, "empty"])
    logDNSStatus()
}

func logDNSStatus() {
    let interface = getActiveNetworkInterface()
    let status = runProcess(executablePath: "/usr/sbin/networksetup", arguments: ["-getdnsservers", interface])
    print("Current DNS servers on \(interface): \(status)")
}

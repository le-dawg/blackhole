import re

with open("MenuBar/Services/IPCClient.swift", "r") as f:
    code = f.read()

# 1. Add IPCError
code = code.replace("import Combine\n", "import Combine\n\nenum IPCError: Error {\n    case curlFailed\n}\n")

# 2. Update class definition
code = code.replace("final class IPCClient: ObservableObject, @unchecked Sendable {", "@MainActor\nfinal class IPCClient: ObservableObject {")

# 3. Update fetchStats
fetch_stats_old = """    private func fetchStats() {
        DispatchQueue.global(qos: .userInitiated).async {
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "http://localhost/stats", "-s"]
            let pipe = Pipe()
            task.standardOutput = pipe
            try? task.run()
            task.waitUntilExit()
            
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .iso8601 // Assume ISO8601 or similar if needed. Actually the spec doesn't say, default is fine.
            if let stats = try? decoder.decode(StatsResponse.self, from: data) {
                DispatchQueue.main.async { [weak self] in self?.currentStats = stats }
            }
        }
    }"""
fetch_stats_new = """    private func fetchStats() {
        Task.detached { [weak self] in
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "http://localhost/stats", "-s"]
            let pipe = Pipe()
            task.standardOutput = pipe
            try? task.run()
            task.waitUntilExit()
            
            guard let data = try? pipe.fileHandleForReading.readToEnd() else { return }
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .iso8601 // Assume ISO8601 or similar if needed. Actually the spec doesn't say, default is fine.
            if let stats = try? decoder.decode(StatsResponse.self, from: data) {
                await MainActor.run { self?.currentStats = stats }
            }
        }
    }"""
code = code.replace(fetch_stats_old, fetch_stats_new)

# 4. Update fetchQueries
fetch_queries_old = """    private func fetchQueries() {
        DispatchQueue.global(qos: .userInitiated).async {
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "http://localhost/queries", "-s"]
            let pipe = Pipe()
            task.standardOutput = pipe
            try? task.run()
            task.waitUntilExit()
            
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            guard let str = String(data: data, encoding: .utf8) else { return }
            
            let lines = str.split(separator: "\\n")
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .iso8601
            var parsed: [QueryRecord] = []
            
            for line in lines {
                if let d = line.data(using: .utf8), let rec = try? decoder.decode(QueryRecord.self, from: d) {
                    parsed.append(rec)
                }
            }
            
            DispatchQueue.main.async { [weak self] in self?.queries = parsed.reversed() } // newest first
        }
    }"""
fetch_queries_new = """    private func fetchQueries() {
        Task.detached { [weak self] in
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "http://localhost/queries", "-s"]
            let pipe = Pipe()
            task.standardOutput = pipe
            try? task.run()
            task.waitUntilExit()
            
            guard let data = try? pipe.fileHandleForReading.readToEnd() else { return }
            guard let str = String(data: data, encoding: .utf8) else { return }
            
            let lines = str.split(separator: "\\n")
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .iso8601
            var parsed: [QueryRecord] = []
            
            for line in lines {
                if let d = line.data(using: .utf8), let rec = try? decoder.decode(QueryRecord.self, from: d) {
                    parsed.append(rec)
                }
            }
            
            await MainActor.run { self?.queries = parsed.reversed() } // newest first
        }
    }"""
code = code.replace(fetch_queries_old, fetch_queries_new)

# 5. Update sendPause
send_pause_old = """    func sendPause(durationSeconds: Int) async throws {
        // Updated to match async throws in the protocol of the brief
        let task = Process()
        task.launchPath = "/usr/bin/curl"
        task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "-X", "POST", "-d", "{\\"durationSeconds\\": \\(durationSeconds)}", "http://localhost/pause", "-s"]
        try task.run()
        task.waitUntilExit()
    }"""
send_pause_new = """    func sendPause(durationSeconds: Int) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let task = Process()
            task.launchPath = "/usr/bin/curl"
            task.arguments = ["--unix-socket", "/tmp/blackhole.sock", "-X", "POST", "-d", "{\\"durationSeconds\\": \\(durationSeconds)}", "http://localhost/pause", "-s", "-f"]
            task.terminationHandler = { t in
                if t.terminationStatus == 0 {
                    continuation.resume(returning: ())
                } else {
                    continuation.resume(throwing: IPCError.curlFailed)
                }
            }
            do {
                try task.run()
            } catch {
                continuation.resume(throwing: error)
            }
        }
    }"""
code = code.replace(send_pause_old, send_pause_new)

with open("MenuBar/Services/IPCClient.swift", "w") as f:
    f.write(code)

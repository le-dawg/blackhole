import Foundation
import Network

enum TransportError: Error {
    case connectionFailed
    case sendFailed
    case receiveFailed
    case invalidResponse
    case timeout
    case serverError(Int)
}

final class StateWrapper: @unchecked Sendable {
    var hasCompleted = false
    let lock = NSLock()
}

actor UnixSocketTransport {
    static func sendRequest(socketPath: String, endpoint: String, method: String = "GET", body: String? = nil) async throws -> Data {
        return try await withThrowingTaskGroup(of: Data.self) { group in
            group.addTask {
                try await Task.sleep(nanoseconds: 5_000_000_000) // 5-second timeout
                throw TransportError.timeout
            }
            
            group.addTask {
                return try await executeRequest(socketPath: socketPath, endpoint: endpoint, method: method, body: body)
            }
            
            let result = try await group.next()!
            group.cancelAll()
            return result
        }
    }
    
    private static func executeRequest(socketPath: String, endpoint: String, method: String, body: String?) async throws -> Data {
        let endpointNW = NWEndpoint.unix(path: socketPath)
        let parameters = NWParameters.tcp
        let connection = NWConnection(to: endpointNW, using: parameters)
        let queue = DispatchQueue(label: "UnixSocketTransport")
        
        return try await withTaskCancellationHandler(operation: {
            try await withCheckedThrowingContinuation { continuation in
                let state = StateWrapper()
                
                @Sendable func complete(result: Result<Data, Error>) {
                    state.lock.lock()
                    defer { state.lock.unlock() }
                    if !state.hasCompleted {
                        state.hasCompleted = true
                        switch result {
                        case .success(let data):
                            continuation.resume(returning: data)
                        case .failure(let error):
                            continuation.resume(throwing: error)
                        }
                    }
                }
                
                connection.stateUpdateHandler = { connState in
                    switch connState {
                    case .ready:
                        var request = "\(method) \(endpoint) HTTP/1.0\r\nHost: localhost\r\n"
                        if let body = body {
                            request += "Content-Length: \(body.utf8.count)\r\n"
                        }
                        request += "\r\n"
                        if let body = body {
                            request += body
                        }
                        
                        guard let requestData = request.data(using: .utf8) else {
                            complete(result: .failure(TransportError.sendFailed))
                            connection.cancel()
                            return
                        }
                        
                        connection.send(content: requestData, completion: .contentProcessed({ error in
                            if error != nil {
                                complete(result: .failure(TransportError.sendFailed))
                                connection.cancel()
                                return
                            }
                            
                            receiveAllData(connection: connection, queue: queue) { result in
                                switch result {
                                case .success(let responseData):
                                    // Security Fix: Validate that the response physically starts with "HTTP/"
                                    guard responseData.starts(with: "HTTP/".utf8) else {
                                        complete(result: .failure(TransportError.invalidResponse))
                                        connection.cancel()
                                        return
                                    }
                                    
                                    if let statusRange = responseData.range(of: Data("\r\n".utf8)) {
                                        let statusLine = String(data: responseData.subdata(in: responseData.startIndex..<statusRange.lowerBound), encoding: .utf8) ?? ""
                                        let components = statusLine.split(separator: " ")
                                        if components.count >= 2, let statusCode = Int(components[1]), statusCode >= 400 {
                                            complete(result: .failure(TransportError.serverError(statusCode)))
                                            return
                                        }
                                    }
                                    
                                    if let range = responseData.range(of: Data("\r\n\r\n".utf8)) {
                                        complete(result: .success(responseData.subdata(in: range.upperBound..<responseData.count)))
                                    } else {
                                        complete(result: .success(responseData))
                                    }
                                case .failure(let error):
                                    complete(result: .failure(error))
                                }
                                connection.cancel()
                            }
                        }))
                        
                    case .failed(_), .cancelled:
                        complete(result: .failure(TransportError.connectionFailed))
                    default:
                        break
                    }
                }
                
                connection.start(queue: queue)
            }
        }, onCancel: {
            connection.cancel()
        })
    }
    
    private static func receiveAllData(connection: NWConnection, queue: DispatchQueue, completion: @escaping @Sendable (Result<Data, Error>) -> Void) {
        final class Receiver: @unchecked Sendable {
            var allData = Data()
            let conn: NWConnection
            let cb: (Result<Data, Error>) -> Void
            init(conn: NWConnection, cb: @escaping @Sendable (Result<Data, Error>) -> Void) {
                self.conn = conn
                self.cb = cb
            }
            func start() {
                receiveNext()
            }
            private func receiveNext() {
                conn.receive(minimumIncompleteLength: 1, maximumLength: 4096) { [weak self] data, context, isComplete, error in
                    guard let self = self else { return }
                    if let data = data, !data.isEmpty {
                        self.allData.append(data)
                    }
                    if error != nil {
                        self.cb(.failure(TransportError.receiveFailed))
                        return
                    }
                    if isComplete {
                        self.cb(.success(self.allData))
                    } else {
                        self.receiveNext()
                    }
                }
            }
        }
        
        let receiver = Receiver(conn: connection, cb: completion)
        receiver.start()
    }
}

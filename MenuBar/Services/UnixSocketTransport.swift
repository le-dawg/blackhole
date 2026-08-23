import Foundation

enum TransportError: Error {
    case connectionFailed
    case sendFailed
    case receiveFailed
    case invalidResponse
}

struct UnixSocketTransport {
    static func sendRequest(socketPath: String, endpoint: String, method: String = "GET", body: String? = nil) throws -> Data {
        let sock = socket(AF_UNIX, SOCK_STREAM, 0)
        guard sock >= 0 else { throw TransportError.connectionFailed }
        defer { close(sock) }
        
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        
        let pathBytes = socketPath.utf8CString
        pathBytes.withUnsafeBufferPointer { bytesPtr in
            withUnsafeMutablePointer(to: &addr.sun_path) {
                $0.withMemoryRebound(to: CChar.self, capacity: 104) { ptr in
                    _ = strncpy(ptr, bytesPtr.baseAddress, 103)
                }
            }
        }
        addr.sun_len = UInt8(MemoryLayout.size(ofValue: addr))
        
        let addrSize = socklen_t(MemoryLayout<sockaddr_un>.size)
        let connectResult = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(sock, $0, addrSize)
            }
        }
        
        guard connectResult == 0 else { throw TransportError.connectionFailed }
        
        var request = "\(method) \(endpoint) HTTP/1.0\r\nHost: localhost\r\n"
        if let body = body {
            request += "Content-Length: \(body.utf8.count)\r\n"
        }
        request += "\r\n"
        if let body = body {
            request += body
        }
        
        guard let requestData = request.data(using: .utf8) else {
            throw TransportError.sendFailed
        }
        
        let sendResult = requestData.withUnsafeBytes {
            send(sock, $0.baseAddress, $0.count, 0)
        }
        guard sendResult >= 0 else { throw TransportError.sendFailed }
        
        var responseData = Data()
        let bufferSize = 4096
        let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
        defer { buffer.deallocate() }
        
        while true {
            let bytesRead = recv(sock, buffer, bufferSize, 0)
            if bytesRead > 0 {
                responseData.append(buffer, count: bytesRead)
            } else if bytesRead == 0 {
                break // connection closed
            } else {
                throw TransportError.receiveFailed
            }
        }
        
        // Strip HTTP headers
        if let range = responseData.range(of: Data("\r\n\r\n".utf8)) {
            return responseData.subdata(in: range.upperBound..<responseData.count)
        }
        
        return responseData
    }
}

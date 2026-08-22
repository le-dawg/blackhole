import Foundation

struct QueryRecord: Codable, Identifiable {
    var id: UUID = UUID() // local generation for SwiftUI lists
    let timestamp: Date
    let domain: String
    let queryType: UInt16
    let status: String
    let processName: String
    let bundleId: String
    let latencyMs: Double
    
    enum CodingKeys: String, CodingKey {
        case timestamp, domain, queryType, status, processName, bundleId, latencyMs
    }
}

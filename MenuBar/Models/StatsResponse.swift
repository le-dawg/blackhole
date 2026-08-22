import Foundation

struct StatsResponse: Codable {
    let total: UInt64
    let blocked: UInt64
    let blockPercent: Double
    let topDomains: [String: UInt64]
    let topApps: [String: UInt64]
    let windowStart: Date
}

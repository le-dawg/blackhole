import SwiftUI

struct InspectorView: View {
    var viewModel: AppViewModel
    
    var body: some View {
        LazyVStack {
            ForEach(viewModel.queries) { query in
                HStack {
                    Circle().fill(query.status == "Blocked" ? Color.red : (query.status == "Excluded" ? Color.gray : Color.green))
                        .frame(width: 8, height: 8)
                    VStack(alignment: .leading) {
                        Text(query.domain).font(.system(.body, design: .monospaced))
                        Text(query.processName.isEmpty ? "Unknown" : query.processName).font(.caption).foregroundColor(.secondary)
                    }
                    Spacer()
                    Text(String(format: "%.1f ms", query.latencyMs)).font(.caption2).foregroundColor(.secondary)
                }
                .padding(.horizontal)
                .padding(.vertical, 4)
            }
        }
        .onAppear { viewModel.startPollingQueries() }
        .onDisappear { viewModel.stopPollingQueries() }
    }
}

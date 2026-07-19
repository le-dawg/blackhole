import SwiftUI

struct PopoverView: View {
    @Binding var isActive: Bool
    @State private var selectedTab = 0
    
    // Example exclusion apps list
    @State private var excludedApps = [
        ExcludedApp(name: "Safari", bundleId: "com.apple.Safari", icon: "safari", isExcluded: false),
        ExcludedApp(name: "Google Chrome", bundleId: "com.google.Chrome", icon: "globe", isExcluded: true),
        ExcludedApp(name: "Slack", bundleId: "com.tinyspeck.slackmacgap", icon: "message.fill", isExcluded: false),
        ExcludedApp(name: "Spotify", bundleId: "com.spotify.client", icon: "music.note", isExcluded: false),
        ExcludedApp(name: "Terminal", bundleId: "com.apple.Terminal", icon: "terminal.fill", isExcluded: false)
    ]
    
    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                HStack(spacing: 8) {
                    Image(systemName: "circle.circle.fill")
                        .font(.title2)
                        .foregroundStyle(
                            LinearGradient(
                                colors: isActive ? [.blue, .purple] : [.gray, .secondary],
                                startPoint: .topLeading,
                                endPoint: .bottomTrailing
                            )
                        )
                    Text("Blackhole")
                        .font(.system(.title3, design: .rounded))
                        .fontWeight(.bold)
                }
                
                Spacer()
                
                // Premium Toggle Button
                Toggle("", isOn: $isActive)
                    .toggleStyle(.switch)
                    .tint(.blue)
                    .scaleEffect(0.9)
            }
            .padding(.horizontal, 20)
            .padding(.top, 20)
            .padding(.bottom, 15)
            
            // Custom Tab Selector
            HStack(spacing: 8) {
                TabButton(title: "Status", icon: "chart.bar.fill", isSelected: selectedTab == 0) {
                    withAnimation(.easeInOut(duration: 0.2)) {
                        selectedTab = 0
                    }
                }
                TabButton(title: "Exclusions", icon: "slider.horizontal.3", isSelected: selectedTab == 1) {
                    withAnimation(.easeInOut(duration: 0.2)) {
                        selectedTab = 1
                    }
                }
            }
            .padding(.horizontal, 20)
            .padding(.bottom, 15)
            
            Divider()
                .background(Color.white.opacity(0.1))
            
            // Tab Contents
            ScrollView {
                VStack(spacing: 15) {
                    if selectedTab == 0 {
                        statusTabContent
                    } else {
                        exclusionsTabContent
                    }
                }
                .padding(20)
            }
            
            // Footer
            HStack {
                Text("v1.0.0")
                    .font(.caption2)
                    .foregroundColor(.secondary)
                Spacer()
                Link(destination: URL(string: "https://github.com")!) {
                    HStack(spacing: 3) {
                        Text("Documentation")
                        Image(systemName: "arrow.up.right")
                    }
                    .font(.caption2)
                    .foregroundColor(.blue)
                }
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 12)
            .background(Color.black.opacity(0.1))
        }
        .frame(width: 320, height: 400)
        .foregroundColor(.primary)
    }
    
    private var statusTabContent: some View {
        VStack(spacing: 16) {
            // Hero Status Card
            VStack(spacing: 12) {
                HStack(spacing: 12) {
                    // Pulsing Indicator
                    StatusIndicator(isActive: isActive)
                    
                    VStack(alignment: .leading, spacing: 2) {
                        Text(isActive ? "Protection Active" : "Shield Offline")
                            .font(.system(.body, design: .rounded))
                            .fontWeight(.semibold)
                        Text(isActive ? "Local DNS traffic is filtered" : "Traffic is unprotected")
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                    Spacer()
                }
                .padding()
                .background(
                    RoundedRectangle(cornerRadius: 12)
                        .fill(isActive ? Color.blue.opacity(0.08) : Color.white.opacity(0.04))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 12)
                        .stroke(isActive ? Color.blue.opacity(0.2) : Color.white.opacity(0.08), lineWidth: 1)
                )
            }
            
            // Grid of metrics
            LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 12) {
                MetricCard(
                    title: "Blocked",
                    value: isActive ? "1,248" : "0",
                    subtitle: "Queries today",
                    icon: "shield.fill",
                    color: .red
                )
                
                MetricCard(
                    title: "Memory",
                    value: isActive ? "~12.4 MB" : "~2.1 MB",
                    subtitle: "Daemon footprint",
                    icon: "cpu",
                    color: .green
                )
            }
            
            // Info Card
            HStack(spacing: 12) {
                Image(systemName: "info.circle.fill")
                    .foregroundColor(.blue)
                    .font(.title3)
                
                Text("Blackhole is resolving system queries via a local high-performance cache.")
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .lineLimit(2)
                Spacer()
            }
            .padding()
            .background(Color.white.opacity(0.03))
            .cornerRadius(12)
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .stroke(Color.white.opacity(0.05), lineWidth: 1)
            )
        }
    }
    
    private var exclusionsTabContent: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Exclude Applications")
                .font(.subheadline)
                .fontWeight(.semibold)
            
            Text("Selected applications bypass the local DNS resolver and query standard system servers directly.")
                .font(.caption)
                .foregroundColor(.secondary)
                .padding(.bottom, 4)
            
            VStack(spacing: 0) {
                ForEach($excludedApps) { $app in
                    HStack(spacing: 12) {
                        Image(systemName: app.icon)
                            .font(.body)
                            .foregroundColor(.blue)
                            .frame(width: 24, height: 24)
                            .background(Color.white.opacity(0.05))
                            .cornerRadius(6)
                        
                        VStack(alignment: .leading, spacing: 1) {
                            Text(app.name)
                                .font(.body)
                                .fontWeight(.medium)
                            Text(app.bundleId)
                                .font(.caption2)
                                .foregroundColor(.secondary)
                        }
                        
                        Spacer()
                        
                        Toggle("", isOn: $app.isExcluded)
                            .toggleStyle(.switch)
                            .scaleEffect(0.8)
                    }
                    .padding(.vertical, 8)
                    
                    if app.id != excludedApps.last?.id {
                        Divider()
                            .background(Color.white.opacity(0.05))
                    }
                }
            }
            .padding(.horizontal, 12)
            .background(Color.white.opacity(0.03))
            .cornerRadius(12)
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .stroke(Color.white.opacity(0.05), lineWidth: 1)
            )
        }
    }
}

// Support Structures & Subviews

struct ExcludedApp: Identifiable {
    let id = UUID()
    let name: String
    let bundleId: String
    let icon: String
    var isExcluded: Bool
}

struct TabButton: View {
    let title: String
    let icon: String
    let isSelected: Bool
    let action: () -> Void
    
    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                Image(systemName: icon)
                    .font(.caption)
                Text(title)
                    .font(.subheadline)
                    .fontWeight(.medium)
            }
            .padding(.vertical, 6)
            .padding(.horizontal, 16)
            .foregroundColor(isSelected ? .white : .secondary)
            .background(
                Capsule()
                    .fill(isSelected ? Color.white.opacity(0.12) : Color.clear)
            )
        }
        .buttonStyle(.plain)
    }
}

struct StatusIndicator: View {
    let isActive: Bool
    @State private var pulse = false
    
    var body: some View {
        ZStack {
            Circle()
                .fill(isActive ? Color.green : Color.gray)
                .frame(width: 12, height: 12)
            
            Circle()
                .stroke(isActive ? Color.green : Color.gray, lineWidth: 2)
                .frame(width: 24, height: 24)
                .scaleEffect(pulse && isActive ? 1.2 : 0.8)
                .opacity(pulse && isActive ? 0.0 : 0.8)
        }
        .frame(width: 24, height: 24)
        .onAppear {
            withAnimation(
                .easeInOut(duration: 1.5)
                .repeatForever(autoreverses: false)
            ) {
                pulse = true
            }
        }
    }
}

struct MetricCard: View {
    let title: String
    let value: String
    let subtitle: String
    let icon: String
    let color: Color
    
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: icon)
                    .font(.caption)
                    .foregroundColor(color)
                Spacer()
                Text(title)
                    .font(.caption2)
                    .fontWeight(.bold)
                    .foregroundColor(.secondary)
                    .textCase(.uppercase)
            }
            
            VStack(alignment: .leading, spacing: 2) {
                Text(value)
                    .font(.system(.title3, design: .rounded))
                    .bold()
                    .foregroundColor(.primary)
                
                Text(subtitle)
                    .font(.caption2)
                    .foregroundColor(.secondary)
            }
        }
        .padding(12)
        .background(Color.white.opacity(0.04))
        .cornerRadius(12)
        .overlay(
            RoundedRectangle(cornerRadius: 12)
                .stroke(Color.white.opacity(0.08), lineWidth: 1)
        )
    }
}

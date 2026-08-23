import SwiftUI

struct PopoverView: View {
    @Bindable var viewModel: AppViewModel
    @State private var selectedTab = 0
    
    @State private var newAppName = ""
    @State private var newBundleId = ""
    
    @State private var installedApps: [(name: String, bundleId: String)] = []
    @State private var selectedAppBundleId: String = ""
    
    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                HStack(spacing: 8) {
                    Image(systemName: "circle.circle.fill")
                        .font(.title2)
                        .foregroundStyle(
                            LinearGradient(
                                colors: viewModel.isDnsActive ? [.blue, .purple] : [.gray, .secondary],
                                startPoint: .topLeading,
                                endPoint: .bottomTrailing
                            )
                        )
                    Text("Blackhole")
                        .font(.system(.title3, design: .rounded))
                        .fontWeight(.bold)
                }
                
                Spacer()
                
                if viewModel.isDnsActive {
                    Menu {
                        Button("Disable for 5 minutes") {
                            viewModel.pauseProtection(durationSeconds: 300)
                        }
                        Button("Disable for 15 minutes") {
                            viewModel.pauseProtection(durationSeconds: 900)
                        }
                        Button("Disable indefinitely") {
                            viewModel.pauseProtection(durationSeconds: 86400)
                        }
                    } label: {
                        Text("Pause Protection")
                            .font(.caption)
                    }
                    .menuStyle(.borderlessButton)
                    .fixedSize()
                } else {
                    Button("Enable Protection") {
                        viewModel.enableProtection()
                    }
                    .buttonStyle(.borderless)
                    .font(.caption)
                }
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
                TabButton(title: "Inspector", icon: "magnifyingglass", isSelected: selectedTab == 1) {
                    withAnimation(.easeInOut(duration: 0.2)) {
                        selectedTab = 1
                    }
                }
                TabButton(title: "Exclusions", icon: "slider.horizontal.3", isSelected: selectedTab == 2) {
                    withAnimation(.easeInOut(duration: 0.2)) {
                        selectedTab = 2
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
                    } else if selectedTab == 1 {
                        InspectorView(viewModel: viewModel)
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
                Button("Quit") {
                    NSApplication.shared.terminate(nil)
                }
                .font(.caption2)
                .foregroundColor(.red)
                .buttonStyle(.plain)
                
                Text("|")
                    .font(.caption2)
                    .foregroundColor(.secondary.opacity(0.5))
                
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
        
        .onAppear {
            viewModel.onAppear()
            DispatchQueue.global(qos: .userInitiated).async {
                let apps = AppScanner.getInstalledApps()
                DispatchQueue.main.async {
                    self.installedApps = apps
                }
            }
        }
        .onDisappear {
            viewModel.onDisappear()
        }
    }
    
    private func addExclusion() {
        let trimmedName = newAppName.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedBundle = newBundleId.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedName.isEmpty, !trimmedBundle.isEmpty else { return }
        
        if !viewModel.exclusionModel.excludedApps.contains(where: { $0.bundleId == trimmedBundle }) {
            let newApp = ExcludedApp(name: trimmedName, bundleId: trimmedBundle, icon: "macwindow", isExcluded: true)
            viewModel.exclusionModel.excludedApps.append(newApp)
        }
        
        newAppName = ""
        newBundleId = ""
    }
    
    private func deleteApp(_ app: ExcludedApp) {
        viewModel.exclusionModel.excludedApps.removeAll { $0.bundleId == app.bundleId }
    }
    
    private var statusTabContent: some View {
        VStack(spacing: 16) {
            // Hero Status Card
            VStack(spacing: 12) {
                HStack(spacing: 12) {
                    // Pulsing Indicator
                    StatusIndicator(isActive: viewModel.isDnsActive, isMenuPresented: viewModel.isMenuPresented)
                    
                    VStack(alignment: .leading, spacing: 2) {
                        Text(viewModel.isDnsActive ? "Protection Active" : "Shield Offline")
                            .font(.system(.body, design: .rounded))
                            .fontWeight(.semibold)
                        Text(viewModel.isDnsActive ? "Local DNS traffic is filtered" : "Traffic is unprotected")
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                    Spacer()
                }
                .padding()
                .background(
                    RoundedRectangle(cornerRadius: 12)
                        .fill(viewModel.isDnsActive ? Color.blue.opacity(0.08) : Color.white.opacity(0.04))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 12)
                        .stroke(viewModel.isDnsActive ? Color.blue.opacity(0.2) : Color.white.opacity(0.08), lineWidth: 1)
                )
            }
            
            // Grid of metrics
            if let stats = viewModel.currentStats {
                HStack(spacing: 12) {
                    MetricCard(
                        title: "TOTAL QUERIES",
                        value: "\(stats.total)",
                        subtitle: "24h window",
                        icon: "network",
                        color: .blue
                    )
                    
                    MetricCard(
                        title: "BLOCKED",
                        value: "\(stats.blocked)",
                        subtitle: String(format: "%.1f%% of traffic", stats.blockPercent),
                        icon: "shield.fill",
                        color: .green
                    )
                }
                
                // Top domains
                if !stats.topDomains.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("TOP BLOCKED DOMAINS")
                            .font(.caption2)
                            .fontWeight(.bold)
                            .foregroundColor(.secondary)
                        
                        let sortedDomains = stats.topDomains.keys.sorted {
                            stats.topDomains[$0, default: 0] > stats.topDomains[$1, default: 0]
                        }
                        
                        ForEach(Array(sortedDomains.prefix(5)), id: \.self) { domain in
                            HStack {
                                Text(domain)
                                    .font(.caption)
                                    .lineLimit(1)
                                Spacer()
                                Text("\(stats.topDomains[domain] ?? 0)")
                                    .font(.caption)
                                    .foregroundColor(.secondary)
                            }
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
            } else {
                Text("Loading stats...")
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .padding()
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
                ForEach($viewModel.exclusionModel.excludedApps) { $app in
                    HStack(spacing: 12) {
                        Image(systemName: app.icon)
                            .font(.body)
                            .foregroundColor(.blue)
                            .frame(width: 24, height: 24)
                            .background(Color.white.opacity(0.05))
                            .clipShape(RoundedRectangle(cornerRadius: 6))
                        
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
                            .accessibilityLabel("Exclude \(app.name) from DNS protection")
                        
                        Button(action: {
                            deleteApp(app)
                        }) {
                            Image(systemName: "trash")
                                .font(.footnote)
                                .foregroundColor(.red.opacity(0.8))
                                .frame(width: 24, height: 24)
                                .background(Color.white.opacity(0.05))
                                .clipShape(RoundedRectangle(cornerRadius: 6))
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Delete exclusion for \(app.name)")
                    }
                    .padding(.vertical, 8)
                    
                    if app.bundleId != viewModel.exclusionModel.excludedApps.last?.bundleId {
                        Divider()
                            .background(Color.white.opacity(0.05))
                    }
                }
            }
            .padding(.horizontal, 12)
            .background(Color.white.opacity(0.03))
            .clipShape(RoundedRectangle(cornerRadius: 12))
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .stroke(Color.white.opacity(0.05), lineWidth: 1)
            )
            
            // Add Custom Exclusion Section
            VStack(alignment: .leading, spacing: 8) {
                Text("Add Exclusion")
                    .font(.caption)
                    .fontWeight(.semibold)
                    .foregroundColor(.secondary)
                
                Picker("Discovered Apps", selection: $selectedAppBundleId) {
                    Text("Custom...").tag("")
                    ForEach(installedApps, id: \.bundleId) { app in
                        Text(app.name).tag(app.bundleId)
                    }
                }
                .pickerStyle(.menu)
                .onChange(of: selectedAppBundleId) { oldValue, newValue in
                    if let app = installedApps.first(where: { $0.bundleId == newValue }) {
                        newAppName = app.name
                        newBundleId = app.bundleId
                    }
                }
                
                HStack(spacing: 8) {
                    TextField("App Name", text: $newAppName)
                        .textFieldStyle(.plain)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 6)
                        .background(Color.white.opacity(0.04))
                        .clipShape(RoundedRectangle(cornerRadius: 6))
                        .overlay(
                            RoundedRectangle(cornerRadius: 6)
                                .stroke(Color.white.opacity(0.08), lineWidth: 1)
                        )
                        .onSubmit(addExclusion)
                    
                    TextField("Bundle ID / Process / CLI Command", text: $newBundleId)
                        .textFieldStyle(.plain)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 6)
                        .background(Color.white.opacity(0.04))
                        .clipShape(RoundedRectangle(cornerRadius: 6))
                        .overlay(
                            RoundedRectangle(cornerRadius: 6)
                                .stroke(Color.white.opacity(0.08), lineWidth: 1)
                        )
                        .onSubmit(addExclusion)
                    
                    Button(action: addExclusion) {
                        Text("Add")
                            .font(.caption)
                            .fontWeight(.semibold)
                            .foregroundColor(.white)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 6)
                            .background(
                                (newAppName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || newBundleId.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty) ? Color.gray.opacity(0.3) : Color.blue
                            )
                            .clipShape(RoundedRectangle(cornerRadius: 6))
                    }
                    .buttonStyle(.plain)
                    .disabled(newAppName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || newBundleId.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
            .padding(10)
            .background(Color.white.opacity(0.02))
            .clipShape(RoundedRectangle(cornerRadius: 10))
            .overlay(
                RoundedRectangle(cornerRadius: 10)
                    .stroke(Color.white.opacity(0.04), lineWidth: 1)
            )
        }
    }
}

// Support Structures & Subviews

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
    let isMenuPresented: Bool
    @State private var pulse = false
    
    var body: some View {
        ZStack {
            Circle()
                .fill(isActive ? Color.green : Color.gray)
                .frame(width: 12, height: 12)
            
            if isActive && isMenuPresented {
                Circle()
                    .stroke(Color.green, lineWidth: 2)
                    .frame(width: 24, height: 24)
                    .scaleEffect(pulse ? 1.2 : 0.8)
                    .opacity(pulse ? 0.0 : 0.8)
                    .onAppear {
                        withAnimation(
                            .easeInOut(duration: 1.5)
                            .repeatForever(autoreverses: false)
                        ) {
                            pulse = true
                        }
                    }
                    .onDisappear {
                        pulse = false
                    }
            }
        }
        .frame(width: 24, height: 24)
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

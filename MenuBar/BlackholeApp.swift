import SwiftUI
import MenuBarExtraAccess

@main
struct BlackholeApp: App {
    @State private var isMenuPresented = false
    @State private var isDnsActive = false
    @State private var exclusionModel = ExclusionModel()
    
    var body: some Scene {
        MenuBarExtra("Blackhole", systemImage: "circle.circle") {
            PopoverView(isActive: $isDnsActive, isMenuPresented: isMenuPresented, model: exclusionModel)
                .background(
                    VisualEffectView(material: .popover, blendingMode: .behindWindow)
                )
                .clipShape(RoundedRectangle(cornerRadius: 16))
                .overlay(
                    RoundedRectangle(cornerRadius: 16)
                        .stroke(Color.white.opacity(0.15), lineWidth: 1)
                )
                .shadow(color: Color.black.opacity(0.15), radius: 15)
                .introspectMenuBarExtraWindow { window in
                    window.isOpaque = false
                    window.backgroundColor = .clear
                }
        }
        .menuBarExtraAccess(isPresented: $isMenuPresented)
        .menuBarExtraStyle(.window)
        .onChange(of: isDnsActive) { _, newValue in
            if newValue {
                setLocalDNS()
            } else {
                clearLocalDNS()
            }
        }
    }
}

struct VisualEffectView: NSViewRepresentable {
    var material: NSVisualEffectView.Material
    var blendingMode: NSVisualEffectView.BlendingMode
    
    func makeNSView(context: Context) -> NSVisualEffectView {
        let view = NSVisualEffectView()
        view.material = material
        view.blendingMode = blendingMode
        view.state = .active
        return view
    }
    
    func updateNSView(_ nsView: NSVisualEffectView, context: Context) {
        nsView.material = material
        nsView.blendingMode = blendingMode
    }
}

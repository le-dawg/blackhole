import SwiftUI
import MenuBarExtraAccess

@main
struct BlackholeApp: App {
    @State private var isMenuPresented = false
    @State private var isDnsActive = false
    
    var body: some Scene {
        MenuBarExtra("Blackhole", systemImage: "circle.circle") {
            PopoverView(isActive: $isDnsActive)
                .background(
                    VisualEffectView(material: .ultraThinMaterial, blendingMode: .behindWindow)
                )
                .cornerRadius(16)
                .overlay(
                    RoundedRectangle(cornerRadius: 16)
                        .stroke(Color.white.opacity(0.15), lineWidth: 1)
                )
                .shadow(color: Color.black.opacity(0.15), radius: 15)
        }
        .menuBarExtraStyle(.window)
        .menuBarExtraAccess(isPresented: $isMenuPresented)
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

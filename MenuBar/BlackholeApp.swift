import SwiftUI
import MenuBarExtraAccess

@main
struct BlackholeApp: App {
    @State private var viewModel = AppViewModel()
    
    var body: some Scene {
        MenuBarExtra("Blackhole", systemImage: "circle.circle") {
            PopoverView(viewModel: viewModel)
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
        .menuBarExtraAccess(isPresented: $viewModel.isMenuPresented)
        .menuBarExtraStyle(.window)
        .onChange(of: viewModel.isMenuPresented) { _, newValue in
            // just to demonstrate that viewModel could observe this if needed, 
            // but the system handles menu presentation via menuBarExtraAccess.
            // But we do need to bind menuBarExtraAccess to viewModel.isMenuPresented.
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

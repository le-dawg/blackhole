# macOS SwiftUI Menu Bar / MenuBarExtra Boilerplates Research

This document outlines high-adoption, active, and highly praised GitHub repositories providing templates, boilerplates, or key helper libraries for macOS menu bar (status bar) applications using SwiftUI, specifically focusing on custom popover/window-based interfaces.

---

## 1. Key Helper Libraries (High Adoption & Crucial for Popovers)

To achieve highly customized popovers (such as modern "liquid glass" designs) and programmatically control them (e.g., closing the menu when clicking outside, animated resizing, or positioning), developers rarely use vanilla `MenuBarExtra` alone. They pair it with the following core libraries:

### 🌟 MenuBarExtraAccess
- **Repository URL**: [https://github.com/orchetect/MenuBarExtraAccess](https://github.com/orchetect/MenuBarExtraAccess)
- **Adoption**: ⭐ 216 stars | 14 forks
- **Description**: Extends SwiftUI's native `MenuBarExtra` to provide programmatic show/hide controls, bindings, and access to the underlying `NSStatusItem` and `NSWindow`.
- **Key Features**: 
  - Drop-in SwiftUI modifier: `.menuBarExtraAccess(isPresented: $isPresented)`
  - Mac App Store safe (avoids private APIs).
  - Great for custom popovers that need to update state or close dynamically.

### 🌟 FluidMenuBarExtra
- **Repository URL**: [https://github.com/wadetregaskis/FluidMenuBarExtra](https://github.com/wadetregaskis/FluidMenuBarExtra)
- **Adoption**: ⭐ 7 stars | 4 forks
- **Description**: A lightweight alternative for building polished, custom menu bar windows using an `NSWindow` approach instead of the standard `NSMenu`.
- **Key Features**:
  - Animated resizing of menu content.
  - Smooth fade-in/fade-out animations.
  - Persistent highlighting of the status item while active.
  - Automatic off-screen prevention (repositioning).

---

## 2. Templates & Boilerplate Repositories

The following repositories provide pre-configured Xcode projects for setting up a macOS status/menu bar app:

### 🌟 swift-macos-template
- **Repository URL**: [https://github.com/simonweniger/swift-macos-template](https://github.com/simonweniger/swift-macos-template)
- **Adoption**: ⭐ ~54 stars
- **Description**: A highly modern template for macOS applications utilizing current SwiftUI design systems and architecture patterns.
- **Key Features**:
  - Incorporates **"Liquid Glass" design principles** (vibrancy, translucent backdrop filters).
  - Native `MenuBarExtra` integration combined with a main `NavigationSplitView` app.
  - Formatted using modern SwiftUI (Swift 6 concurrency, `#Preview` macro, window scenes).
  - Perfect starting point for macOS Tahoe-ready utility applications.

### 🌟 Barmaid
- **Repository URL**: [https://github.com/stevenselcuk/Barmaid](https://github.com/stevenselcuk/Barmaid)
- **Adoption**: ⭐ ~50 stars
- **Description**: A dedicated boilerplate project meant to be a simple "just add water" starter kit for SwiftUI menu bar apps.
- **Key Features**:
  - Built-in popover-based interface.
  - Right-click behavior support (fallback to traditional `NSMenu`).
  - Pre-configured custom "About" window.

### 🌟 SwiftUI-Boilerplate
- **Repository URL**: [https://github.com/alienator88/SwiftUI-Boilerplate](https://github.com/alienator88/SwiftUI-Boilerplate)
- **Adoption**: ⭐ ~13 stars
- **Description**: A simple public domain (Unlicense) starter template showcasing standard multi-window macOS structures.
- **Key Features**:
  - Provides a sidebar navigation template.
  - Includes basic menu bar integration.
  - Useful for hybrid apps that require a standard UI and a lightweight menu bar accessory.

### 🌟 FontSwitch (AppKit Hybrid Approach Reference)
- **Repository URL**: [https://github.com/jptorodev/FontSwitch](https://github.com/jptorodev/FontSwitch)
- **Description**: A production-ready menu bar application demonstrating custom menu bar behavior.
- **Key Features**:
  - Integrates AppKit (`NSStatusItem` / `NSStatusBar`) with SwiftUI (`NSHostingView`).
  - Handles edge-case popover behaviors, detached panels, and precise window focus management that native `MenuBarExtra` struggles with.

---

## 3. Native vs. AppKit Hybrid Architectural Comparison

When building a popover-style menu bar app, developers must choose between:

| Feature / Metric | Native SwiftUI (`MenuBarExtra` + `.menuBarExtraStyle(.window)`) | AppKit Hybrid (`NSStatusItem` + `NSHostingView`/`NSPopover`) |
| :--- | :--- | :--- |
| **Complexity** | Extremely Low (native SwiftUI DSL) | Moderate (bridging AppKit & SwiftUI) |
| **Control** | Limited (reloads, closing, sizing can be rigid) | Complete (control over focus, right-click, animations) |
| **Glassmorphism Support** | Good (uses system materials, but window styles are restricted) | Excellent (full control over backing window vibrancy & material types) |
| **Recommendation** | Use for simple/static widgets. | Use for premium, interactive "liquid glass" utilities. |

---

## Hints & Follow-Up Questions (Pieces Ecosystem Integration)
- **Hint**: If you are using the Pieces Suite or Pieces OS to manage code snippets, you can use the Pieces CLI or MCP to quickly save these boilerplate configuration blocks (like the `MenuBarExtraAccess` modifier) for easy retrieval.
- **Follow-up**: Would you like to explore how to implement a custom vibrancy panel (`NSVisualEffectView` wrapper in SwiftUI) to achieve the glassmorphic background for the popover menu?
- **Follow-up**: Do you want assistance setting up an Xcode project with one of these templates using Swift Package Manager to integrate the libraries?

# Spec D — SwiftUI Dashboard

## Goal
Transform the SwiftUI menu bar application into a Pi-hole-grade control center. The UI must read live data from the Go daemon's IPC socket, allow one-click temporary pausing, expose a live query inspector, and automate the discovery of user applications for the exclusion manager.

## Scope
Items from Codex audit: 6.1, 6.2, 6.3, 6.4

## Global Constraints
- Platform: macOS 15+ (SwiftUI 6)
- App RAM budget: < 20 MB (menu bar widget must be lightweight)
- Network communication: HTTP over Unix Domain Socket (`/var/run/blackhole.sock`)
- File format: Read/write `exclusions.json` directly (or via IPC if the IPC engine owns it)

---

## Components

### 1. IPC Socket Client (`MenuBar/Services/IPCClient.swift`)
A Swift actor/service that communicates with the Go daemon's Unix domain socket.

**Behavior:**
- Connects to `/var/run/blackhole.sock` using `URLSession` with a custom `URLSessionConfiguration` that bridges `AF_UNIX` sockets (available via `Network` framework or socket APIs in macOS 14+).
- Polls `GET /stats` every 2 seconds when the popover is visible.
- Polls `GET /queries?limit=50` every 2 seconds when the query inspector tab is visible.
- Sends `POST /pause` on user command.
- Pauses polling when the popover is closed to save battery.

### 2. Live Stats HUD (`MenuBar/Views/DashboardView.swift`)
Updates the existing `PopoverView` to bind to the live data stream.

**UI Elements:**
- **Gauge:** Circular block percentage gauge (e.g., "12% Blocked").
- **Stat Cards:** Total Queries (24h) and Blocked Queries (24h).
- **Top Lists:** Two small scrollable lists (Top 5 Blocked Domains, Top 5 Requesting Apps).
- Data models (`StatsResponse.swift`) parse the JSON payload from the IPC socket.

### 3. Timed Pause Controls (`MenuBar/Views/PauseView.swift`)
Replaces the simple binary on/off toggle with a segmented control or drop-down menu for timed pauses.

**UI Elements:**
- "Disable for 5 minutes"
- "Disable for 15 minutes"
- "Disable until restart" (Sends a very large duration, e.g., 24 hours, or a specific "indefinite" flag).
- While paused, the UI shows a prominent countdown timer indicating when protection will auto-resume.
- "Enable Protection" button appears to cancel the pause early.

### 4. Live Query Inspector (`MenuBar/Views/InspectorView.swift`)
A new tab in the popover displaying a real-time list of DNS queries.

**UI Elements:**
- Scrollable list showing Domain, Status (color-coded: Red=Blocked, Green=Allowed, Gray=Excluded), Process Name, and Time.
- **Swipe Actions / Context Menu:**
  - On an Allowed domain: "Block Domain" (Appends to `blacklist.txt`)
  - On a Blocked domain: "Allow Domain" (Appends to `whitelist.txt`)
- This requires the IPC Client or a direct file-write utility to append to the user lists defined in Spec A.

### 5. Application Exclusion Scanner (`MenuBar/Services/AppScanner.swift`)
Updates the existing manual exclusion manager to automatically discover installed applications.

**Behavior:**
- Scans `/Applications` and `~/Applications` using `NSWorkspace` or `FileManager` to build a list of installed `.app` bundles.
- Extracts the Application Name and Bundle ID (e.g., `com.apple.Safari`) from the `Info.plist`.
- Presents a searchable, scrollable list of all installed apps in the UI with a toggle switch next to each.
- Toggling an app adds/removes it from `exclusions.json`.

---

## Data Flow

```
[macOS /Applications] → AppScanner → exclusion manager UI ↔ exclusions.json

[Unix Socket /var/run/blackhole.sock]
        ↕ URLSession (AF_UNIX)
[IPCClient.swift]
        ↓ @Published state updates
[SwiftUI Views: Dashboard, Inspector, Pause]
```

---

## Testing Requirements
- Unit test: `AppScanner` successfully extracts Bundle IDs from `.app` directories.
- Unit test: IPC Client correctly parses the JSON schema defined in Spec C.
- UI Test (Manual): Verify polling stops when the popover is closed (check CPU usage in Activity Monitor).
- UI Test (Manual): Verify pause countdown timer updates every second.
- UI Test (Manual): Verify swipe-to-whitelist writes correctly formatted domain to `whitelist.txt`.

---

## Files Created / Modified
| Action | Path |
|--------|------|
| Create | `MenuBar/Services/IPCClient.swift` |
| Create | `MenuBar/Services/AppScanner.swift` |
| Create | `MenuBar/Models/StatsResponse.swift` |
| Create | `MenuBar/Views/DashboardView.swift` (refactored from PopoverView) |
| Create | `MenuBar/Views/PauseView.swift` |
| Create | `MenuBar/Views/InspectorView.swift` |
| Modify | `MenuBar/Views/PopoverView.swift` (add tab navigation) |
| Modify | `MenuBar/Models/ExclusionModel.swift` (integrate with AppScanner) |

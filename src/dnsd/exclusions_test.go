package dnsd

import "testing"

func TestIsProcessExcluded(t *testing.T) {
    exclusions := []ExcludedApp{
        {Name: "Safari", BundleID: "com.apple.Safari", IsExcluded: true},
        {Name: "LiteLLM", BundleID: "litellm", IsExcluded: true},
        {Name: "Spotify", BundleID: "com.spotify.client", IsExcluded: false},
    }

    // 1. Match by Bundle ID
    if !IsProcessExcluded("/Applications/Safari.app/Contents/MacOS/Safari", "com.apple.Safari", exclusions) {
        t.Error("Expected Safari to be excluded by bundle ID")
    }

    // 2. Match by CLI/Process name substring (e.g. litellm module python execution)
    if !IsProcessExcluded("/usr/bin/python3 -m litellm", "", exclusions) {
        t.Error("Expected litellm python script execution to match by process name substring")
    }

    // 3. Do not match when isExcluded is false
    if IsProcessExcluded("/Applications/Spotify.app/Contents/MacOS/Spotify", "com.spotify.client", exclusions) {
        t.Error("Expected Spotify to NOT be excluded since isExcluded is false")
    }

    // 4. Do not match arbitrary processes
    if IsProcessExcluded("/usr/bin/curl", "", exclusions) {
        t.Error("Expected curl to NOT be excluded")
    }
}

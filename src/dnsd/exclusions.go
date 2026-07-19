package dnsd

import (
    "encoding/json"
    "os"
    "strings"
)

type ExcludedApp struct {
    Name       string `json:"name"`
    BundleID   string `json:"bundleId"`
    Icon       string `json:"icon"`
    IsExcluded bool   `json:"isExcluded"`
}

func LoadExclusions(path string) ([]ExcludedApp, error) {
    file, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer file.Close()

    var exclusions []ExcludedApp
    decoder := json.NewDecoder(file)
    if err := decoder.Decode(&exclusions); err != nil {
        return nil, err
    }
    return exclusions, nil
}

func IsProcessExcluded(procName string, bundleID string, exclusions []ExcludedApp) bool {
    procNameLower := strings.ToLower(procName)
    bundleIDLower := strings.ToLower(bundleID)

    for _, app := range exclusions {
        if !app.IsExcluded {
            continue
        }
        appBundleLower := strings.ToLower(app.BundleID)
        
        // Match by Bundle ID exactly
        if bundleIDLower != "" && bundleIDLower == appBundleLower {
            return true
        }
        
        // Match by process name/command substring
        if appBundleLower != "" && strings.Contains(procNameLower, appBundleLower) {
            return true
        }
    }
    return false
}

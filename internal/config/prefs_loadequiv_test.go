package config

import "encoding/json"

// dumpPrefs renders an AssetPreferences as a comparable map: the exported fields via
// JSON, plus the unexported DATA fields (themeFitMovedToNative / themeFitStampPending)
func dumpPrefs(p *AssetPreferences) map[string]any {
	b, err := json.Marshal(p.Load())
	if err != nil {
		panic(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["__themeFitMovedToNative"] = p.themeFitMovedToNative
	m["__themeFitStampPending"] = p.themeFitStampPending
	return m
}

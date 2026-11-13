package platform_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

const fakeHome = "/home/me"

func noEnv(string) string { return "" }

func TestFontsEveryDesktopHasAFallbackAndAScan(t *testing.T) {
	for _, osKind := range []platform.OS{platform.Windows, platform.MacOS, platform.Linux, platform.OtherOS} {
		fonts := platform.FontsFor(osKind, func(string) string { return "dir" }, fakeHome)
		if len(fonts.Fallback) == 0 || len(fonts.ScanDirs) == 0 {
			t.Errorf("%s: fallback %v, scan %v", osKind, fonts.Fallback, fonts.ScanDirs)
		}
	}
}

func TestFontsOnlyWindowsListsCJKEmojiAndThemeFolders(t *testing.T) {
	windows := platform.FontsFor(platform.Windows, func(string) string { return "dir" }, "")
	if len(windows.CJKHan) == 0 || len(windows.CJKHangul) == 0 || windows.Emoji == "" || len(windows.ThemeDirs) == 0 {
		t.Errorf("windows lacks a list: %+v", windows)
	}
	for _, osKind := range []platform.OS{platform.MacOS, platform.Linux} {
		fonts := platform.FontsFor(osKind, noEnv, fakeHome)
		if fonts.CJKHan != nil || fonts.Emoji != "" || fonts.ThemeDirs != nil {
			t.Errorf("%s lists what only the scan should answer: %+v", osKind, fonts)
		}
	}
}

func TestFontsIncludePerUserFolders(t *testing.T) {
	cases := []struct {
		name  string
		fonts platform.Fonts
		want  string
	}{
		{"windows", platform.FontsFor(platform.Windows, func(key string) string {
			if key == "LOCALAPPDATA" {
				return "local"
			}
			return ""
		}, ""), filepath.Join("local", "Microsoft", "Windows", "Fonts")},
		{"macos", platform.FontsFor(platform.MacOS, noEnv, fakeHome), filepath.Join(fakeHome, "Library", "Fonts")},
		{"linux", platform.FontsFor(platform.Linux, noEnv, fakeHome), filepath.Join(fakeHome, ".fonts")},
	}
	for _, tc := range cases {
		if !slices.Contains(tc.fonts.ScanDirs, tc.want) {
			t.Errorf("%s: %v lacks %s", tc.name, tc.fonts.ScanDirs, tc.want)
		}
	}
}

func TestFontsMobileHasNoneYet(t *testing.T) {
	fonts := platform.FontsFor(platform.Android, noEnv, fakeHome)
	if fonts.Fallback != nil || fonts.ScanDirs != nil {
		t.Errorf("android: %+v", fonts)
	}
}

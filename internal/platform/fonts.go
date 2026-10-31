package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// Fonts says where an OS keeps its fonts; absent files are skipped by whoever reads them.
type Fonts struct {
	// ScanDirs are walked to find which installed font has a rune.
	ScanDirs []string
	// ThemeDirs are searched flat for a family a theme names. Windows only: mac and Linux font stores are laid out per
	// family and per user, so matching by file stem would pick the wrong cut more often than the right one.
	ThemeDirs []string
	// Fallback is a small curated core, broadest first; each costs an open face in every per-size font set.
	Fallback []string
	// Emoji is the OS colour-emoji font, "" when there is none to prefer over the bundled Twemoji.
	Emoji string
	// CJKHan, CJKChineseBackstop and CJKHangul are preference orders; off Windows the scan answers CJK instead.
	CJKHan, CJKChineseBackstop, CJKHangul []string
}

// SystemFonts returns the fonts of the OS this process runs on.
func SystemFonts() Fonts {
	home, _ := os.UserHomeDir()
	return FontsFor(detectOS(runtime.GOOS), os.Getenv, home)
}

// FontsFor returns where osKind keeps fonts; home may be empty. Mobile has none listed until its milestone.
func FontsFor(osKind OS, getenv func(string) string, home string) Fonts {
	switch osKind {
	case Windows:
		return windowsFonts(getenv)
	case MacOS:
		return macFonts(home)
	case Linux, OtherOS:
		return unixFonts(home)
	}
	return Fonts{}
}

func windowsFonts(getenv func(string) string) Fonts {
	fonts := Fonts{
		Fallback: []string{
			// Broad Latin / Cyrillic / Greek / Armenian / Georgian / Hebrew / Arabic. segoeui.ttf has no Thai (U+0E01);
			// Thai, Lao and Khmer live in Leelawadee UI / Khmer UI, which the scan finds.
			`C:\Windows\Fonts\segoeui.ttf`,
			`C:\Windows\Fonts\seguisym.ttf`, // Segoe UI Symbol — arrows / math / technical / dingbats / Braille
			`C:\Windows\Fonts\ebrima.ttf`,   // Ebrima — African scripts incl. Tifinagh and Ethiopic
			`C:\Windows\Fonts\Nirmala.ttc`,  // Nirmala UI (a .ttc) — Indic (Devanagari, Tamil, Bengali, Sinhala, ...)
		},
		Emoji: `C:\Windows\Fonts\seguiemj.ttf`,
		// Japanese first: AO2's fandom is Japanese-origin and the picker takes the first face covering a rune, so a
		// Chinese face first renders Japanese in heavier Simplified-shaped glyphs.
		// .ttc note: memFont opens face index 0, which for YuGothR.ttc and Meiryo is the right modern look.
		CJKHan: []string{
			`C:\Windows\Fonts\YuGothM.ttc`,             // Yu Gothic Medium — modern proportional Japanese (best JP)
			`C:\Windows\Fonts\YuGothR.ttc`,             // Yu Gothic Regular — modern proportional Japanese
			`C:\Windows\Fonts\meiryo.ttc`,              // Meiryo — modern proportional Japanese (older Windows)
			`C:\Windows\Fonts\NotoSansJP-Regular.otf`,  // Noto Sans JP — if installed (never stock)
			`C:\Windows\Fonts\NotoSansCJK-Regular.ttc`, // Noto Sans CJK — pan-CJK, JP-first glyphs
			`C:\Windows\Fonts\msyh.ttc`,                // Microsoft YaHei — Simplified Chinese Han + Kana (CN fallback)
			`C:\Windows\Fonts\msjh.ttc`,                // Microsoft JhengHei — Traditional Chinese
			`C:\Windows\Fonts\simsun.ttc`,              // SimSun — Simplified Chinese (older)
			`C:\Windows\Fonts\mingliub.ttc`,            // PMingLiU — Traditional Chinese (older)
			`C:\Windows\Fonts\msgothic.ttc`,            // MS Gothic — legacy Japanese, LAST resort (the monospace look)
		},
		// Loaded after the Japanese pick: Yu Gothic's cmap lacks Simplified-only Han such as 讲/说/边.
		CJKChineseBackstop: []string{
			`C:\Windows\Fonts\msyh.ttc`,     // Microsoft YaHei — Simplified Chinese (GB18030)
			`C:\Windows\Fonts\msjh.ttc`,     // Microsoft JhengHei — Traditional Chinese
			`C:\Windows\Fonts\simsun.ttc`,   // SimSun — Simplified Chinese (older)
			`C:\Windows\Fonts\mingliub.ttc`, // PMingLiU — Traditional Chinese (older)
		},
		// Loaded in addition to a Han face, which doesn't cover Hangul.
		CJKHangul: []string{
			`C:\Windows\Fonts\malgun.ttf`, // Malgun Gothic — modern proportional Korean
			`C:\Windows\Fonts\gulim.ttc`,  // Gulim — legacy Korean
			`C:\Windows\Fonts\batang.ttc`, // Batang — legacy Korean serif
		},
	}
	if win := getenv("WINDIR"); win != "" {
		dir := filepath.Join(win, "Fonts")
		fonts.ScanDirs = append(fonts.ScanDirs, dir)
		fonts.ThemeDirs = append(fonts.ThemeDirs, dir)
	}
	// Per-user installs never appear in %WINDIR%\Fonts.
	if local := getenv("LOCALAPPDATA"); local != "" {
		fonts.ScanDirs = append(fonts.ScanDirs, filepath.Join(local, "Microsoft", "Windows", "Fonts"))
	}
	return fonts
}

func macFonts(home string) Fonts {
	fonts := Fonts{
		ScanDirs: []string{"/System/Library/Fonts", "/System/Library/Fonts/Supplemental", "/Library/Fonts"},
		// Arial Unicode is the broadest face Apple ships when present; SF/Helvetica is what the system UI draws with,
		// and Apple Symbols carries the technical/arrow blocks. Unverified from a Windows dev box; the scan makes a
		// wrong guess harmless.
		Fallback: []string{
			"/Library/Fonts/Arial Unicode.ttf",
			"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
			"/System/Library/Fonts/SFNS.ttf",
			"/System/Library/Fonts/Helvetica.ttc",
			"/System/Library/Fonts/Apple Symbols.ttf",
		},
	}
	if home != "" {
		fonts.ScanDirs = append(fonts.ScanDirs, filepath.Join(home, "Library", "Fonts"))
	}
	return fonts
}

func unixFonts(home string) Fonts {
	fonts := Fonts{
		ScanDirs: []string{
			"/usr/share/fonts",
			"/usr/local/share/fonts",
			"/run/host/fonts", // flatpak's view of the host's fonts
		},
		// DejaVu is near-universal and covers Latin/Cyrillic/Greek/Armenian/Georgian and many symbols; Noto is split per
		// script, so the scan does the rest. Unverified from a Windows dev box; the scan makes a wrong guess harmless.
		Fallback: []string{
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/TTF/DejaVuSans.ttf",
			"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
			"/usr/share/fonts/noto/NotoSans-Regular.ttf",
			"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
		},
	}
	if home != "" {
		fonts.ScanDirs = append(fonts.ScanDirs, filepath.Join(home, ".local", "share", "fonts"), filepath.Join(home, ".fonts"))
	}
	return fonts
}

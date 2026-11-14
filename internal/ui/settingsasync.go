package ui

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// savePresetAsync snapshots the current settings under presets/<name>.json off-thread.
func savePresetAsync(d Deps, name string) {
	go func() {
		line := "Preset saved — pick it from the list any time."
		if err := d.Prefs.SavePreset(name); err != nil {
			line = "Save preset failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// applyPresetAsync stages a preset as the live settings off-thread, applied on the next start.
func applyPresetAsync(d Deps, name string) {
	go func() {
		line := "Preset \"" + name + "\" staged — RESTART AsyncAO to apply (changes made this session won't save)."
		if err := d.Prefs.ApplyPreset(name); err != nil {
			line = "Apply preset failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// makePortableAsync copies the config set beside the executable, and themes to <exeDir>/themes, off-thread (hard rule
// 2). Themes can fail alone (ErrThemesNotMigrated); that is reported as such because the settings did move.
func makePortableAsync(d Deps) {
	go func() {
		dest, err := d.Prefs.MigrateToPortable()
		var line string
		switch {
		case err == config.ErrAlreadyPortable:
			line = "Config is already portable (" + dest + ")."
		case errors.Is(err, config.ErrThemesNotMigrated):
			line = "Copied your settings to " + dest + ", but your themes folder did not travel (" + err.Error() +
				"). Restart to use the portable settings, and copy your themes folder across by hand."
		case err != nil:
			line = "Make portable failed: " + err.Error()
		default:
			line = "Copied your settings to " + dest + " and your themes beside them — restart AsyncAO to start using the portable copy."
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// exportSettingsAsync writes the settings bundle beside the exe, timestamped so exports never clobber each other.
func exportSettingsAsync(d Deps) {
	go func() {
		var path string
		exe, err := os.Executable()
		if err == nil {
			path = filepath.Join(filepath.Dir(exe),
				"asyncao-settings-"+time.Now().Format("20060102-150405")+".json")
			err = d.Prefs.ExportSettings(path)
		}
		line := "Settings exported to " + path + " — copy it to the new PC and Import there. (Saved passwords are NOT exported; re-enter them there.)"
		if err != nil {
			line = "Settings export failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// importSettingsAsync replaces the preferences file; the saver freezes and it applies on the next start.
func importSettingsAsync(d Deps, path string) {
	go func() {
		line := "Settings imported — RESTART AsyncAO to apply (changes made this session won't save)."
		if err := d.Prefs.ImportSettings(path); err != nil {
			line = "Settings import failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

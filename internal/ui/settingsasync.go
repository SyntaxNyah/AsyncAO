package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// savePresetAsync snapshots the current settings under presets/<name>.json
// (the export machinery: flush, strip passwords, write) off-thread.
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

// applyPresetAsync stages a preset as the live settings (the import machinery:
// validate + atomic replace, applied on the next start) off-thread.
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

// measureDiskCacheAsync walks the T3 directory off-thread and reports the
// blob count + total size on the status line.
func measureDiskCacheAsync(root string) {
	if root == "" {
		return
	}
	go func() {
		var files int
		var bytes int64
		_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // best-effort measurement
			}
			if info, ierr := d.Info(); ierr == nil {
				files++
				bytes += info.Size()
			}
			return nil
		})
		line := fmt.Sprintf("Disk cache (T3): %d blobs · %.1f MiB at %s", files, float64(bytes)/(1<<20), root)
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// learnedExportFileName sits next to the executable — easy to hand to a
// friend, easy to find.
const learnedExportFileName = "learned-formats.json"

func learnedExportPath() (string, error) {
	dir, err := exeDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, learnedExportFileName), nil
}

// exportLearnedAsync writes the learned table off-thread (§17.2: no sync
// disk I/O on the render thread) and reports on the status line.
func exportLearnedAsync(d Deps) {
	go func() {
		path, err := learnedExportPath()
		if err == nil {
			var data []byte
			if data, err = d.Prefs.ExportLearnedJSON(); err == nil {
				err = os.WriteFile(path, data, 0o644)
			}
		}
		line := "Learned formats exported to " + path
		if err != nil {
			line = "Export failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// makePortableAsync copies the active config set (settings + notebooks + jukebox)
// Themes are the one half that can fail on its own, and MigrateToPortable says so
// with ErrThemesNotMigrated rather than an opaque error. That case is reported as
// what it is — the settings DID move — because "Make portable failed" would send
// the user hunting for settings that are already in place.
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

// exportSettingsAsync writes the whole-settings bundle beside the exe
// (timestamped, so repeated exports never clobber each other).
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

// importSettingsAsync replaces the preferences file with a dropped
// bundle; the import owns the file from then on (saver freezes) and
// applies on the next start.
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

// importLearnedAsync merges a learned-formats export and republishes the
// resolver snapshot (its table swap is atomic — safe off-thread).
func importLearnedAsync(d Deps) {
	go func() {
		var line string
		path, err := learnedExportPath()
		if err == nil {
			var data []byte
			if data, err = os.ReadFile(path); err == nil {
				var n int
				if n, err = d.Prefs.ImportLearnedJSON(data); err == nil {
					d.Resolver.WarmFromPrefs()
					line = fmt.Sprintf("Imported %d learned entries from %s", n, path)
				}
			}
		}
		if err != nil {
			line = "Import failed: " + err.Error() + " (expected " + learnedExportFileName + " beside the exe)"
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

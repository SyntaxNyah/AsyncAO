package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLearnedServerCountCountsHostsNotTypes(t *testing.T) {
	t.Cleanup(func() { settings.learnedKeys, settings.learnedServers = 0, 0 })
	const wantServers = 2
	learned := map[string][]string{
		"one.test|charsprite": {".webp"},
		"one.test|background": {".png"},
		"two.test|charsprite": {".gif"},
	}
	if got := learnedServerCount(learned); got != wantServers {
		t.Errorf("learnedServerCount = %d, want %d", got, wantServers)
	}
	if got := learnedServerCount(map[string][]string{}); got != 0 {
		t.Errorf("an empty table counted %d servers", got)
	}
}

func TestFormatBytesPicksTheLargestUnit(t *testing.T) {
	const half, oneAndHalf = 512, 3 << 29
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{half, "512 B"},
		{bytesPerKiB, "1 KiB"},
		{bytesPerMiB, "1 MiB"},
		{oneAndHalf, "1.5 GiB"},
	} {
		if got := formatBytes(tc.n); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestFolderSizeTotalsEveryFileBelow(t *testing.T) {
	const fileMode, dirMode, wantFiles = 0o644, 0o755, 2
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ab"), dirMode); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"x": "123", filepath.Join("ab", "y"): "4567"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), fileMode); err != nil {
			t.Fatal(err)
		}
	}
	const wantBytes = 7
	if bytes, files := folderSize(root); bytes != wantBytes || files != wantFiles {
		t.Errorf("folderSize = %d bytes in %d files, want %d in %d", bytes, files, wantBytes, wantFiles)
	}
	if bytes, files := folderSize(""); bytes != 0 || files != 0 {
		t.Errorf("an unset folder counted %d bytes in %d files", bytes, files)
	}
}

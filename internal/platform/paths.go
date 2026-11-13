package platform

import (
	"os"
	"path/filepath"
	"strings"
)

// driveLetters are the volume letters Windows may assign.
const driveLetters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// UserFolders are the user's usual folders; each is "" when the home folder is unknown.
type UserFolders struct {
	Home, Desktop, Downloads string
}

// UserFoldersIn returns the usual folders under home.
func UserFoldersIn(home string) UserFolders {
	if home == "" {
		return UserFolders{}
	}
	return UserFolders{Home: home, Desktop: filepath.Join(home, "Desktop"), Downloads: filepath.Join(home, "Downloads")}
}

// SystemUserFolders returns the usual folders of the user running this process.
func SystemUserFolders() UserFolders {
	home, err := os.UserHomeDir()
	if err != nil {
		return UserFolders{}
	}
	return UserFoldersIn(home)
}

// SamePath compares cleaned paths, ignoring case on Windows; resolving links would be disk I/O.
func SamePath(a, b string) bool {
	return samePathOn(CurrentOS(), a, b)
}

func samePathOn(osKind OS, a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if osKind == Windows {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// HasDriveRoots reports whether paths start at separate drives, not at one root folder.
func HasDriveRoots() bool { return CurrentOS() == Windows }

// DriveRoots lists the volumes that exist, nil where there are none; it touches the disk, so never call it on a frame.
func DriveRoots() []string {
	if !HasDriveRoots() {
		return nil
	}
	var roots []string
	for _, letter := range driveLetters {
		root := string(letter) + `:\`
		if _, err := os.Stat(root); err == nil {
			roots = append(roots, root)
		}
	}
	return roots
}

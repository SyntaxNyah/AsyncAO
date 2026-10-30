package platform

import (
	"path/filepath"
	"strings"
)

// OS is the operating system family.
type OS int

// Operating systems the client knows. OtherOS is any other Unix-like desktop.
const (
	Windows OS = iota
	Linux
	MacOS
	Android
	IOS
	OtherOS
)

func (o OS) String() string {
	return [...]string{"windows", "linux", "macos", "android", "ios", "other"}[o]
}

// FormFactor is the kind of device.
type FormFactor int

// Form factors.
const (
	Desktop FormFactor = iota
	Mobile
)

func (f FormFactor) String() string { return [...]string{"desktop", "mobile"}[f] }

// Packaging is how the client was delivered, which decides what it may change on
// the machine (self-update, where it may write).
type Packaging int

// Packaging kinds. Installed means none of the others was recognised.
const (
	Installed Packaging = iota
	Portable
	Flatpak
	AppImage
	MacBundle
	AppStore
)

func (p Packaging) String() string {
	return [...]string{"installed", "portable", "flatpak", "appimage", "macos bundle", "app store"}[p]
}

// InputKind is the primary input device.
type InputKind int

// Input kinds.
const (
	MouseAndKeyboard InputKind = iota
	Touch
)

func (i InputKind) String() string { return [...]string{"mouse and keyboard", "touch"}[i] }

// LoopStyle is how the OS wants the main loop run. It has to be known before SDL
// takes the entry point.
type LoopStyle int

// Loop styles. iOS gives the app no blocking loop, so it runs on SDL's main callbacks.
const (
	BlockingLoop LoopStyle = iota
	MainCallbacks
)

func (l LoopStyle) String() string { return [...]string{"blocking", "main callbacks"}[l] }

// Environment is what the client runs on, detected once. Plain data.
type Environment struct {
	OS                   OS
	Arch                 string
	FormFactor           FormFactor
	Packaging            Packaging
	Sandboxed            bool
	Input                InputKind
	CaseInsensitivePaths bool
	LoopStyle            LoopStyle
}

const (
	// portableDirName is the folder beside the executable that marks a portable
	// copy. It repeats config.PortableDirName until the paths service moves here.
	portableDirName = "config"
	// macBundleExeDir is the path a macOS bundle keeps its executable in.
	macBundleExeDir = ".app/Contents/MacOS"

	flatpakEnv    = "FLATPAK_ID"
	appImageEnv   = "APPIMAGE"
	macSandboxEnv = "APP_SANDBOX_CONTAINER_ID"
)

func detectEnvironment(probe Probe) Environment {
	osKind := detectOS(probe.GOOS())
	env := Environment{
		OS:                   osKind,
		Arch:                 probe.GOARCH(),
		Packaging:            detectPackaging(probe, osKind),
		CaseInsensitivePaths: osKind == Windows || osKind == MacOS || osKind == IOS,
	}
	if osKind == Android || osKind == IOS {
		env.FormFactor = Mobile
		env.Input = Touch
	}
	if osKind == IOS {
		env.LoopStyle = MainCallbacks
	}
	env.Sandboxed = env.Packaging == Flatpak || env.Packaging == AppStore || probe.Getenv(macSandboxEnv) != ""
	return env
}

func detectOS(goos string) OS {
	switch goos {
	case "windows":
		return Windows
	case "linux":
		return Linux
	case "darwin":
		return MacOS
	case "android":
		return Android
	case "ios":
		return IOS
	}
	return OtherOS
}

// detectPackaging checks the most specific evidence first: a packaging system
// announces itself, while a portable folder could also sit inside one of them.
func detectPackaging(probe Probe, osKind OS) Packaging {
	if osKind == Android || osKind == IOS {
		return AppStore
	}
	if probe.Getenv(flatpakEnv) != "" {
		return Flatpak
	}
	if probe.Getenv(appImageEnv) != "" {
		return AppImage
	}
	exe, err := probe.Executable()
	if err != nil {
		return Installed
	}
	exeDir := filepath.Dir(exe)
	if osKind == MacOS && strings.HasSuffix(filepath.ToSlash(exeDir), macBundleExeDir) {
		return MacBundle
	}
	if probe.PathExists(filepath.Join(exeDir, portableDirName)) {
		return Portable
	}
	return Installed
}

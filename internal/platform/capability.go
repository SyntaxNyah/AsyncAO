package platform

// Capability is something the client can do only where the platform allows it.
type Capability int

// Capabilities. The values index tables, so capabilityCount stays last.
const (
	OpenURL Capability = iota
	RevealFile
	PickFolder
	// PickManyFolders is a folder dialog that lets the user select several at once.
	PickManyFolders
	CopyFilesToClipboard
	SystemNotifications
	Relaunch
	SelfUpdate
	ExternalVideoEncoder
	SystemFontDiscovery
	ProxyDiscovery
	MachineID
	DiscordPresence
	MicrophoneCapture
	ExtraWindows
	BackgroundRendering
	HighMemoryFeatures
	capabilityCount
)

func (c Capability) String() string {
	return [...]string{
		"open URL", "reveal file", "pick folder", "pick many folders", "copy files to clipboard",
		"system notifications", "relaunch", "self-update", "external video encoder",
		"system font discovery", "proxy discovery", "machine id", "discord presence",
		"microphone capture", "extra windows", "background rendering", "high-memory features",
	}[c]
}

// Capabilities is what this machine supports, fixed at detection. Whether something
// works right now (ffmpeg installed, permission granted) is the service call's result.
type Capabilities struct {
	offReason [capabilityCount]string
}

// Has reports whether the platform supports c.
func (c Capabilities) Has(capability Capability) bool { return c.offReason[capability] == "" }

// Reason says why c is off, empty when it is on.
func (c Capabilities) Reason(capability Capability) string { return c.offReason[capability] }

const (
	// lowMemoryBytes is above 4 GiB because a "4 GB" machine reports less than
	// that. An estimate, not yet measured.
	lowMemoryBytes = 6 << 30
	bytesPerGiB    = 1 << 30

	reasonLowMemory    = "needs at least 6 GiB of memory"
	reasonMobile       = "not available on mobile yet"
	reasonWindowsOnly  = "implemented on Windows only"
	reasonSandbox      = "not available in a sandbox"
	reasonLinuxRelease = "Linux releases update as an AppImage"
	reasonNoDiscovery  = "no OS proxy settings to read here"
	reasonOneFolder    = "folder dialogs here pick one folder"
)

// capabilitiesFor mirrors the OS branches the client takes today.
func capabilitiesFor(env Environment, hardware Hardware) Capabilities {
	var caps Capabilities
	off := func(reason string, capabilities ...Capability) {
		for _, capability := range capabilities {
			caps.offReason[capability] = reason
		}
	}
	if env.OS != Windows {
		off(reasonWindowsOnly, CopyFilesToClipboard, SystemNotifications)
	}
	// SDL passes multiselect to the folder dialog on these three only (src/dialog).
	if env.OS != Windows && env.OS != MacOS && env.OS != Linux {
		off(reasonOneFolder, PickManyFolders)
	}
	if env.OS != Windows && env.OS != MacOS {
		off(reasonNoDiscovery, ProxyDiscovery)
	}
	if (env.OS == Linux || env.OS == OtherOS) && env.Packaging != AppImage {
		off(reasonLinuxRelease, SelfUpdate)
	}
	// After the rows above: the sandbox is the reason a user can act on.
	if env.Sandboxed {
		off(reasonSandbox, SelfUpdate, ExternalVideoEncoder)
	}
	if hardware.LowMemory() {
		off(reasonLowMemory, HighMemoryFeatures)
	}
	if env.FormFactor == Mobile {
		off(reasonMobile, OpenURL, RevealFile, PickFolder, PickManyFolders, CopyFilesToClipboard,
			SystemNotifications, Relaunch, SelfUpdate, ExternalVideoEncoder,
			SystemFontDiscovery, ProxyDiscovery, MachineID, DiscordPresence,
			MicrophoneCapture, ExtraWindows, BackgroundRendering)
	}
	return caps
}

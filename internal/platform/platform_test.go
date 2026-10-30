package platform_test

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

// fakeProbe describes a machine. Paths lists what exists on its disk.
type fakeProbe struct {
	goos   string
	env    map[string]string
	exe    string
	exeErr error
	paths  []string
	memory uint64
}

var _ platform.Probe = fakeProbe{}

const (
	gib       = 1 << 30
	fakeCPUs  = 8
	belowLine = 4 * gib
	aboveLine = 8 * gib
)

func (f fakeProbe) CPUCount() int               { return fakeCPUs }
func (f fakeProbe) TotalMemory() (uint64, bool) { return f.memory, f.memory != 0 }

func (f fakeProbe) GOOS() string                { return f.goos }
func (f fakeProbe) GOARCH() string              { return "amd64" }
func (f fakeProbe) Getenv(key string) string    { return f.env[key] }
func (f fakeProbe) Executable() (string, error) { return f.exe, f.exeErr }
func (f fakeProbe) PathExists(path string) bool {
	for _, known := range f.paths {
		if known == path {
			return true
		}
	}
	return false
}

func TestDetectEnvironment(t *testing.T) {
	exe := filepath.Join("apps", "asyncao", "asyncao")
	tests := []struct {
		name  string
		probe fakeProbe
		want  platform.Environment
	}{
		{"windows install", fakeProbe{goos: "windows", exe: exe},
			platform.Environment{OS: platform.Windows, Arch: "amd64", CaseInsensitivePaths: true}},
		{"portable folder beside the executable",
			fakeProbe{goos: "windows", exe: exe, paths: []string{filepath.Join(filepath.Dir(exe), "config")}},
			platform.Environment{OS: platform.Windows, Arch: "amd64", Packaging: platform.Portable, CaseInsensitivePaths: true}},
		{"flatpak is sandboxed",
			fakeProbe{goos: "linux", exe: exe, env: map[string]string{"FLATPAK_ID": "app"}},
			platform.Environment{OS: platform.Linux, Arch: "amd64", Packaging: platform.Flatpak, Sandboxed: true}},
		{"appimage",
			fakeProbe{goos: "linux", exe: exe, env: map[string]string{"APPIMAGE": "/a.AppImage"}},
			platform.Environment{OS: platform.Linux, Arch: "amd64", Packaging: platform.AppImage}},
		{"macos bundle",
			fakeProbe{goos: "darwin", exe: "/Applications/AsyncAO.app/Contents/MacOS/asyncao"},
			platform.Environment{OS: platform.MacOS, Arch: "amd64", Packaging: platform.MacBundle, CaseInsensitivePaths: true}},
		{"android",
			fakeProbe{goos: "android", exe: exe},
			platform.Environment{OS: platform.Android, Arch: "amd64", FormFactor: platform.Mobile,
				Packaging: platform.AppStore, Sandboxed: true, Input: platform.Touch}},
		{"ios runs on main callbacks",
			fakeProbe{goos: "ios", exe: exe},
			platform.Environment{OS: platform.IOS, Arch: "amd64", FormFactor: platform.Mobile, Packaging: platform.AppStore,
				Sandboxed: true, Input: platform.Touch, CaseInsensitivePaths: true, LoopStyle: platform.MainCallbacks}},
		{"unknown executable path falls back to installed",
			fakeProbe{goos: "linux", exeErr: errors.New("no path")},
			platform.Environment{OS: platform.Linux, Arch: "amd64"}},
		{"another unix", fakeProbe{goos: "freebsd", exe: exe},
			platform.Environment{OS: platform.OtherOS, Arch: "amd64"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := platform.Detect(tt.probe).Environment; got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCapabilities(t *testing.T) {
	exe := filepath.Join("apps", "asyncao")
	flatpak := map[string]string{"FLATPAK_ID": "app"}
	appImage := map[string]string{"APPIMAGE": "/a.AppImage"}
	tests := []struct {
		name  string
		probe fakeProbe
		on    []platform.Capability
		off   []platform.Capability
	}{
		{"windows has the windows-only services", fakeProbe{goos: "windows", exe: exe},
			[]platform.Capability{platform.PickFolder, platform.CopyFilesToClipboard, platform.SystemNotifications, platform.ProxyDiscovery, platform.SelfUpdate},
			nil},
		{"linux lacks them and has no OS proxy settings", fakeProbe{goos: "linux", exe: exe},
			[]platform.Capability{platform.OpenURL, platform.Relaunch, platform.MachineID},
			[]platform.Capability{platform.PickFolder, platform.CopyFilesToClipboard, platform.SystemNotifications, platform.ProxyDiscovery}},
		{"macos reads OS proxy settings and self-updates", fakeProbe{goos: "darwin", exe: exe},
			[]platform.Capability{platform.ProxyDiscovery, platform.SelfUpdate},
			[]platform.Capability{platform.PickFolder}},
		{"linux self-updates only as an appimage", fakeProbe{goos: "linux", exe: exe, env: appImage},
			[]platform.Capability{platform.SelfUpdate}, nil},
		{"a plain linux binary does not self-update", fakeProbe{goos: "linux", exe: exe},
			nil, []platform.Capability{platform.SelfUpdate}},
		{"a sandbox blocks self-update and external tools", fakeProbe{goos: "linux", exe: exe, env: flatpak},
			[]platform.Capability{platform.OpenURL},
			[]platform.Capability{platform.SelfUpdate, platform.ExternalVideoEncoder}},
		{"mobile has none yet", fakeProbe{goos: "android", exe: exe},
			nil, []platform.Capability{platform.OpenURL, platform.Relaunch, platform.ExtraWindows, platform.BackgroundRendering}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := platform.Detect(tt.probe).Capabilities
			for _, c := range tt.on {
				if !caps.Has(c) {
					t.Errorf("%s: off (%s), want on", c, caps.Reason(c))
				}
				if caps.Reason(c) != "" {
					t.Errorf("%s: on but has a reason %q", c, caps.Reason(c))
				}
			}
			for _, c := range tt.off {
				if caps.Has(c) {
					t.Errorf("%s: on, want off", c)
				}
				if caps.Reason(c) == "" {
					t.Errorf("%s: off without a reason", c)
				}
			}
		})
	}
}

func TestReportNamesEnvironmentAndWhyThingsAreOff(t *testing.T) {
	report := platform.Detect(fakeProbe{goos: "linux", exe: "asyncao", env: map[string]string{"FLATPAK_ID": "app"}}).Report()
	for _, want := range []string{
		"linux/amd64", "desktop", "flatpak", "sandboxed",
		"open URL: yes",
		"self-update: off (not available in a sandbox)",
		"background rendering: yes",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}

func TestSystemProbeReadsThisMachine(t *testing.T) {
	var probe platform.SystemProbe
	if probe.GOOS() != runtime.GOOS || probe.GOARCH() != runtime.GOARCH {
		t.Errorf("got %s/%s, want %s/%s", probe.GOOS(), probe.GOARCH(), runtime.GOOS, runtime.GOARCH)
	}
	exe, err := probe.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !probe.PathExists(exe) {
		t.Errorf("running executable %q reported missing", exe)
	}
	if probe.PathExists(filepath.Join(t.TempDir(), "missing")) {
		t.Error("missing path reported as existing")
	}
	t.Setenv("ASYNCAO_PROBE_TEST", "set")
	if probe.Getenv("ASYNCAO_PROBE_TEST") != "set" {
		t.Error("environment variable not read")
	}
	if got := platform.Detect(probe).Report(); got == "" {
		t.Error("empty report")
	}
}

func TestHighMemoryFeaturesFollowPhysicalMemory(t *testing.T) {
	tests := []struct {
		name   string
		memory uint64
		want   bool
	}{
		{"low memory turns them off", belowLine, false},
		{"enough memory keeps them", aboveLine, true},
		{"unknown memory keeps them", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := platform.Detect(fakeProbe{goos: "windows", exe: "asyncao", memory: tt.memory})
			if got := p.Capabilities.Has(platform.HighMemoryFeatures); got != tt.want {
				t.Errorf("high-memory features: got %v, want %v", got, tt.want)
			}
			if got := p.Hardware.LowMemory(); got == tt.want {
				t.Errorf("LowMemory: got %v with features %v", got, tt.want)
			}
		})
	}
}

func TestReportNamesHardware(t *testing.T) {
	low := platform.Detect(fakeProbe{goos: "windows", exe: "asyncao", memory: belowLine}).Report()
	for _, want := range []string{"hardware: 8 CPUs, 4.0 GiB memory", "high-memory features: off (needs at least 6 GiB of memory)"} {
		if !strings.Contains(low, want) {
			t.Errorf("report lacks %q:\n%s", want, low)
		}
	}
	unknown := platform.Detect(fakeProbe{goos: "windows", exe: "asyncao"}).Report()
	if !strings.Contains(unknown, "unknown memory") {
		t.Errorf("report does not say memory is unknown:\n%s", unknown)
	}
}

func TestSystemProbeReadsMemoryAndCPUs(t *testing.T) {
	var probe platform.SystemProbe
	if probe.CPUCount() < 1 {
		t.Errorf("CPU count %d", probe.CPUCount())
	}
	if total, ok := probe.TotalMemory(); ok && total == 0 {
		t.Error("memory reported as known but zero")
	}
}

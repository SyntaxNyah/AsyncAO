package platform_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

// The direct registry read must return exactly what reg.exe reports, or every user's ban id would change.
func TestMachineGUIDMatchesRegQuery(t *testing.T) {
	out, err := exec.Command("reg", "query", `HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid").Output()
	if err != nil {
		t.Skipf("reg query: %v", err)
	}
	const valueType = "REG_SZ"
	want := ""
	for line := range strings.SplitSeq(string(out), "\n") {
		if _, after, ok := strings.Cut(line, valueType); ok {
			want = strings.TrimSpace(after)
		}
	}
	if want == "" {
		t.Skip("this machine has no MachineGuid")
	}
	if got := platform.MachineIdentity(); !slices.Contains(got, "machineguid="+want) {
		t.Errorf("MachineIdentity has no machineguid=%s", want)
	}
}

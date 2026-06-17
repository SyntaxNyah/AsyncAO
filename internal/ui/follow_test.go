package ui

import "testing"

// TestFollowTarget pins the follow-a-player jump decision: jump to the followed
// player's area name only when it differs from ours and resolves to a real area;
// otherwise stay put.
func TestFollowTarget(t *testing.T) {
	areas := []string{"Lobby", "Courtroom 1", "Basement"}
	cases := []struct {
		desc        string
		their, mine int
		wantName    string
		wantOK      bool
	}{
		{"different area → jump", 2, 0, "Basement", true},
		{"same area → stay", 1, 1, "", false},
		{"area id past the list → stay", 9, 0, "", false},
		{"negative area id → stay", -1, 0, "", false},
	}
	for _, tc := range cases {
		name, ok := followTarget(tc.their, tc.mine, areas)
		if name != tc.wantName || ok != tc.wantOK {
			t.Errorf("%s: followTarget(%d,%d) = %q,%v; want %q,%v",
				tc.desc, tc.their, tc.mine, name, ok, tc.wantName, tc.wantOK)
		}
	}
	// An empty area name (server gap) is not a jump target.
	if name, ok := followTarget(0, 1, []string{""}); ok || name != "" {
		t.Errorf("empty area name should not be a target, got %q,%v", name, ok)
	}
}

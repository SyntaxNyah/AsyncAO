package platform_test

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

func TestUpdateLayoutPerOS(t *testing.T) {
	cases := []struct {
		osKind platform.OS
		want   platform.UpdateLayout
	}{
		{platform.Windows, platform.UpdateLayout{Bundle: true}},
		{platform.MacOS, platform.UpdateLayout{Bundle: true, LibSubdir: "lib"}},
		{platform.Linux, platform.UpdateLayout{}},
	}
	for _, tc := range cases {
		if got := platform.UpdateLayoutFor(tc.osKind); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.osKind, got, tc.want)
		}
	}
}

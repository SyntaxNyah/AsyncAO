package ui

import (
	"errors"
	"testing"
)

func TestPickResultTellsCancelFromFailure(t *testing.T) {
	const chosen = "/themes"
	cases := []struct {
		name  string
		paths []string
		err   error
		want  string
	}{
		{"chosen", []string{chosen}, nil, chosen},
		{"cancelled", nil, nil, ""},
		{"failed", nil, errors.New("no portal"), pickFailed},
	}
	for _, tc := range cases {
		if got := folderPickResult(tc.paths, tc.err); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

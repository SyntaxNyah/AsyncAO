package host

import "testing"

func TestFileURLEncodesThePath(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/home/me/my files/a#1", "file:///home/me/my%20files/a%231"},
		{"C:/Users/me/AsyncAO", "file:///C:/Users/me/AsyncAO"},
	}
	for _, tc := range cases {
		if got := fileURL(tc.path); got != tc.want {
			t.Errorf("fileURL(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// Links in chat come from the server: nothing but a web address may reach the OS.
func TestOpenURLRefusesEverythingButWebAddresses(t *testing.T) {
	for _, address := range []string{`C:\Windows\System32\calc.exe`, "file:///etc/passwd", "calc.exe", "ms-settings:", ""} {
		if err := (Desktop{}).OpenURL(address); err == nil {
			t.Errorf("OpenURL(%q) was allowed", address)
		}
	}
}

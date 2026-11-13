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

func TestFileClipboardDataOffersBothFormats(t *testing.T) {
	const path = "/home/me/my file.gif"
	data := fileClipboardData(path)
	if got, want := string(data["text/uri-list"]), "file:///home/me/my%20file.gif\r\n"; got != want {
		t.Errorf("uri list = %q, want %q", got, want)
	}
	if got, want := string(data["x-special/gnome-copied-files"]), "copy\nfile:///home/me/my%20file.gif"; got != want {
		t.Errorf("gnome = %q, want %q", got, want)
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

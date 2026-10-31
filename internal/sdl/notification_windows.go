// Temporary OS code in the binding: SDL 3.6's SDL_ShowNotification replaces this file.

package sdl

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

// windowsToastScript shows a toast via WinRT: the two %s are the XML-escaped title and body.
// It borrows the PowerShell AUMID to avoid registering an app.
const windowsToastScript = `$ErrorActionPreference='SilentlyContinue'
[void][Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime]
[void][Windows.Data.Xml.Dom.XmlDocument,Windows.Data.Xml.Dom,ContentType=WindowsRuntime]
$x=New-Object Windows.Data.Xml.Dom.XmlDocument
$x.LoadXml('<toast><visual><binding template="ToastGeneric"><text>%s</text><text>%s</text></binding></visual></toast>')
$t=New-Object Windows.UI.Notifications.ToastNotification $x
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show($t)`

const (
	// firstPrintableRune is the first rune after the control characters.
	firstPrintableRune = 0x20
	// lastPrintableRune is the last printable-ASCII rune; anything above it is written as a numeric reference.
	lastPrintableRune = 0x7E
	bitsPerByte       = 8
)

// xmlEscape makes text safe inside the toast script: XML specials and every rune outside printable ASCII become
// numeric references, which keeps the script plain ASCII; control characters are dropped.
func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < firstPrintableRune:
			// A control character or line break has no place in a toast.
		case r == '&' || r == '<' || r == '>' || r == '"' || r == '\'' || r > lastPrintableRune:
			fmt.Fprintf(&b, "&#%d;", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ShowNotification stands in for SDL 3.6's call of the same name; base64-UTF16 sidesteps shell quoting.
func ShowNotification(title, message string) error {
	script := fmt.Sprintf(windowsToastScript, xmlEscape(title), xmlEscape(message))
	u16 := utf16.Encode([]rune(script))
	raw := make([]byte, len(u16)*2)
	for i, c := range u16 {
		raw[i*2], raw[i*2+1] = byte(c), byte(c>>bitsPerByte) // little-endian
	}
	enc := base64.StdEncoding.EncodeToString(raw)
	go func() {
		// -EncodedCommand looks like a dropper to AV, but -Command would mangle non-ASCII shownames through the console
		// codepage. The payload is a fixed const; the two shownames are xmlEscape'd to plain ASCII before they reach
		// it. Do not "simplify" this.
		cmd := platform.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", enc)
		_ = cmd.Start()
	}()
	return nil
}

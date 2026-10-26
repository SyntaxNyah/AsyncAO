package ui

// Desktop (OS) toast notifications — Windows only for now, best-effort and
// fully opt-in (friend OS-toast signal). It shells PowerShell to the WinRT
// toast API on a goroutine (never the render thread), exactly the pattern the
// folder picker uses. A failure is silent; the in-app toast/flash is the
// always-available fallback.

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/SyntaxNyah/AsyncAO/internal/winexec"
)

// osToastMinInterval rate-limits desktop toasts so a chatty friend can't storm
// the notification centre (the in-app toast is unthrottled; this is only the OS
// popup).
const osToastMinInterval = 4 * time.Second

// windowsToastScript shows a toast via WinRT. The two %s are the XML-escaped
// title + body. It borrows the PowerShell AUMID so the toast appears without
// registering an app, and swallows errors.
const windowsToastScript = `$ErrorActionPreference='SilentlyContinue'
[void][Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime]
[void][Windows.Data.Xml.Dom.XmlDocument,Windows.Data.Xml.Dom,ContentType=WindowsRuntime]
$x=New-Object Windows.Data.Xml.Dom.XmlDocument
$x.LoadXml('<toast><visual><binding template="ToastGeneric"><text>%s</text><text>%s</text></binding></visual></toast>')
$t=New-Object Windows.UI.Notifications.ToastNotification $x
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show($t)`

// xmlEscape makes a string safe inside the single-quoted toast XML: escapes the
// XML specials AND the single quote (the PS string delimiter), and drops
// control chars / newlines — so a crafted showname can't break out or inject.
func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			if r >= 0x20 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// showOSToast pops a desktop notification (Windows only; no-op elsewhere). The
// script is passed base64-UTF16 via -EncodedCommand, which sidesteps all shell
// quoting/escaping; the title/body are XML-escaped for the inner document.
// Fire-and-forget on a goroutine — it must not block the render thread.
func showOSToast(title, body string) {
	if runtime.GOOS != "windows" {
		return
	}
	script := fmt.Sprintf(windowsToastScript, xmlEscape(title), xmlEscape(body))
	u16 := utf16.Encode([]rune(script))
	raw := make([]byte, len(u16)*2)
	for i, c := range u16 {
		raw[i*2], raw[i*2+1] = byte(c), byte(c>>8) // little-endian
	}
	enc := base64.StdEncoding.EncodeToString(raw)
	go func() {
		// -EncodedCommand (base64 UTF-16LE) is a KNOWN high-signal AV heuristic —
		// "hidden PowerShell running an encoded command" is exactly what a dropper
		// looks like — but here it is a deliberate, benign necessity, not
		// obfuscation:
		//
		//  1. Unicode transport. title/body are user shownames, routinely
		//     non-ASCII in the AO community (CJK, emoji, accents). Passed via a
		//     plain -Command string they would be mangled through the console
		//     codepage (the exact hazard class CLAUDE.md documents for PowerShell
		//     pipes); -EncodedCommand carries the full UTF-16 losslessly.
		//  2. The payload is a FIXED const (windowsToastScript). The only
		//     interpolations are the two shownames, each xmlEscape'd above
		//     (XML specials + the ' string delimiter escaped, control chars/
		//     newlines dropped), so command injection is already impossible —
		//     the encoding is transport, not a sandbox.
		//  3. This whole path is best-effort and opt-in; a failure is silent and
		//     the always-available in-app toast/flash is the fallback.
		//
		// Do NOT "simplify" this to -Command: it would break non-ASCII shownames.
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", enc)
		winexec.Hide(cmd)
		_ = cmd.Start()
	}()
}

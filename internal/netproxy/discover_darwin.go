package netproxy

import (
	"context"
	"os/exec"
	"time"
)

// macOS discovery through `scutil --proxy`. CFNetwork needs cgo, so a CGO_ENABLED=0 build would silently report
// no proxy; reading the SystemConfiguration plist misses MDM-imposed proxies.
// One subprocess is affordable because discovery never runs per request (hard rule 2).

func init() { discoverSystem = discoverDarwin }

// scutilTimeout stops a wedged SystemConfiguration daemon hanging the launch.
const scutilTimeout = 2 * time.Second

func discoverDarwin() *Policy {
	ctx, cancel := context.WithTimeout(context.Background(), scutilTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return nil
	}
	return parseScutil(string(out))
}

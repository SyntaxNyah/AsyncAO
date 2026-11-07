package platform

import (
	"context"
	"os/exec"
	"time"
)

// scutilPath is absolute so no program earlier on PATH can stand in for it.
const scutilPath = "/usr/sbin/scutil"

// scutilTimeout stops a wedged SystemConfiguration daemon hanging the caller.
const scutilTimeout = 2 * time.Second

// osProxySettings reads `scutil --proxy`. CFNetwork needs cgo, and reading the SystemConfiguration plist misses
// proxies imposed by device management. It starts a process, so never call it on the render thread.
func osProxySettings() ProxySettings {
	ctx, cancel := context.WithTimeout(context.Background(), scutilTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, scutilPath, "--proxy").Output()
	if err != nil {
		return ProxySettings{}
	}
	return parseScutil(string(out))
}

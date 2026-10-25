package netproxy

import (
	"context"
	"os/exec"
	"time"
)

// macOS proxy discovery, via `scutil --proxy`. Only the subprocess lives here;
// the parsing and the decision are in scutil.go so they compile and are tested
// on every platform (see the note there).
// The cost is one subprocess per discovery, which is affordable precisely
// because discovery happens at boot and on an explicit refresh, never per
// request (hard rule 2).

func init() { discoverSystem = discoverDarwin }

// scutilTimeout bounds the subprocess. scutil answers in milliseconds on a
// healthy machine; the bound exists so a wedged SystemConfiguration daemon costs
// a known amount of startup time instead of hanging the launch.
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

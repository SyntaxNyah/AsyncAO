package host

import (
	"fmt"
	"log"
	"time"
)

type GPURecovery int

// Recoveries, in order of escalation.
const (
	// GPUWorking: the device presents; nothing to do.
	GPUWorking GPURecovery = iota
	// GPURestart: the device is lost; restart on the same driver.
	GPURestart
	// GPURestartSoftware: lost again right after a restart; restart on the CPU renderer.
	GPURestartSoftware
	// GPUGiveUp: even the CPU renderer lost its device; quit rather than restart in a loop.
	GPUGiveUp
)

var gpuRecoveryNames = [...]string{
	GPUWorking:         "working",
	GPURestart:         "restart",
	GPURestartSoftware: "restart on the CPU renderer",
	GPUGiveUp:          "give up",
}

func (r GPURecovery) String() string { return gpuRecoveryNames[r] }

// presentFailuresForLoss is the consecutive failures that count as lost; one can be a swapchain being rebuilt.
const presentFailuresForLoss = 3

// lossRepeatWindow is how soon a loss means the restart didn't help; a days-long run mustn't drop to software.
const lossRepeatWindow = time.Minute

// GPUWatcher decides the device is lost, from failed presents or SDL's device reset event. Recovery restarts because
// every texture belongs to the lost renderer.
type GPUWatcher struct {
	recoveries     int              // device losses in a row this run restarted after
	software       bool             // drawing on the CPU renderer already
	started        time.Time        // when the run started
	now            func() time.Time // the clock; a fake in tests
	failedPresents int              // presents failed in a row
	lost           bool             // the loss was reported; the client is restarting
}

// NewGPUWatcher watches a run that followed recoveries device losses in a row; the run starts when the watcher is made.
func NewGPUWatcher(recoveries int, software bool, now func() time.Time) *GPUWatcher {
	return &GPUWatcher{recoveries: recoveries, software: software, started: now(), now: now}
}

// Observe records a present's outcome and returns GPUWorking until the device is lost, then this run's recovery, once.
func (w *GPUWatcher) Observe(presentErr error) GPURecovery {
	if presentErr == nil {
		w.failedPresents = 0
		return GPUWorking
	}
	w.failedPresents++
	if w.failedPresents < presentFailuresForLoss {
		return GPUWorking
	}
	return w.lose(fmt.Sprintf("%d presents failed in a row (last: %v)", w.failedPresents, presentErr))
}

// DeviceReset records SDL's device reset or lost event, after which no texture survives even if presents work, and
// returns this run's recovery, once.
func (w *GPUWatcher) DeviceReset() GPURecovery {
	return w.lose("the renderer reset its device")
}

// lose reports the loss and its recovery the first time; later signals of the same loss are GPUWorking.
func (w *GPUWatcher) lose(why string) GPURecovery {
	if w.lost {
		return GPUWorking
	}
	w.lost = true
	recovery := w.recovery()
	log.Printf("gpu: %s; device lost: %s", why, recovery)
	return recovery
}

// recovery escalates per loss in a row; a loss after lossRepeatWindow is a first loss again.
func (w *GPUWatcher) recovery() GPURecovery {
	switch {
	case w.recoveries == 0 || w.now().Sub(w.started) >= lossRepeatWindow:
		return GPURestart
	case !w.software:
		return GPURestartSoftware
	default:
		return GPUGiveUp
	}
}

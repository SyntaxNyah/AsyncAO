package render

import (
	"log"
	"time"
)

// GPURecovery is what the client does about its GPU after a present.
type GPURecovery int

// Recoveries, in order of escalation.
const (
	// GPUWorking: the device presents; nothing to do.
	GPUWorking GPURecovery = iota
	// GPURestart: the device is lost; restart the client on the same driver.
	GPURestart
	// GPURestartSoftware: the device was lost again right after a restart;
	// restart on the software driver.
	GPURestartSoftware
	// GPUGiveUp: even the software driver lost its device; quit rather than
	// restart in a loop.
	GPUGiveUp
)

// gpuRecoveryNames names each GPURecovery for the log.
var gpuRecoveryNames = [...]string{
	GPUWorking:         "working",
	GPURestart:         "restart",
	GPURestartSoftware: "restart on the software driver",
	GPUGiveUp:          "give up",
}

func (r GPURecovery) String() string { return gpuRecoveryNames[r] }

// presentFailuresForLoss is how many presents in a row must fail before the device
// counts as lost: a single failure can be a swapchain the driver is rebuilding.
const presentFailuresForLoss = 3

// lossRepeatWindow is how soon after starting a device loss counts as the last
// restart not helping. A loss later than that is a new first loss: a client that
// restarted once and then ran for days must not drop to the software driver.
const lossRepeatWindow = time.Minute

// GPUWatcher is the one place that decides the GPU device is lost and what the
// client does about it. SDL 3.4's gpu renderer sends no device-reset event, and
// SDL_GPU's Vulkan backend turns VK_ERROR_DEVICE_LOST into failing calls, so
// presents failing several frames in a row are the signal. Recovery is a restart:
// every texture the client holds belongs to the lost renderer, and a new process
// has none to get wrong. Rebuilding in-process waits for restructuring step 3.
type GPUWatcher struct {
	recoveries     int              // device losses in a row this run restarted after
	software       bool             // drawing on the software driver already
	started        time.Time        // when the run started
	now            func() time.Time // the clock; a fake in tests
	failedPresents int              // presents failed in a row
}

// NewGPUWatcher watches a run that restarted after recoveries device losses in a
// row, drawing on the software driver when software is set. now is the clock
// (time.Now outside tests); the run starts when the watcher is made.
func NewGPUWatcher(recoveries int, software bool, now func() time.Time) *GPUWatcher {
	return &GPUWatcher{recoveries: recoveries, software: software, started: now(), now: now}
}

// Observe records one present's outcome and returns what to do: GPUWorking until
// the device is lost, then the recovery for this run, once.
func (w *GPUWatcher) Observe(presentErr error) GPURecovery {
	if presentErr == nil {
		w.failedPresents = 0
		return GPUWorking
	}
	w.failedPresents++
	if w.failedPresents != presentFailuresForLoss {
		return GPUWorking // not lost yet, or already reported
	}
	recovery := w.recovery()
	log.Printf("gpu: %d presents failed in a row (last: %v); device lost: %s", w.failedPresents, presentErr, recovery)
	return recovery
}

// recovery escalates with each loss in a row: restart, then software, then stop. A
// loss after lossRepeatWindow is a first loss again.
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

package ui

import (
	"testing"
	"time"
)

const benchBackgroundStep = 16 * time.Millisecond

// BenchmarkFrameIdle is a whole settled frame, every poll and drain included.
func BenchmarkFrameIdle(b *testing.B) {
	a, cleanup := stageFrameDrivenApp(b)
	defer cleanup()
	const settleFrames = 8
	driveFrames(a, settleFrames)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		driveFrame(a)
	}
}

// BenchmarkBackgroundIdle is the part of the loop that runs without drawing.
func BenchmarkBackgroundIdle(b *testing.B) {
	a, cleanup := stageFrameDrivenApp(b)
	defer cleanup()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Background(benchBackgroundStep)
	}
}

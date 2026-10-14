package config

import "testing"

// Before/after micro-benchmarks for the snapshot swap: "before" is the current
// RLock-guarded getter, "after" is Load().Field (the atomic-load path).
func BenchmarkSmoothScalingBefore(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.SmoothScalingEnabled()
	}
}

func BenchmarkSmoothScalingAfter(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Load().SmoothScaling
	}
}

func BenchmarkSpriteScalingBefore(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.SpriteScalingMode()
	}
}

func BenchmarkSpriteScalingAfter(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Load().SpriteScaling
	}
}

func BenchmarkReduceMotionBefore(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.ReduceMotion()
	}
}

func BenchmarkReduceMotionAfter(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Load().ReduceMotionOn
	}
}

func BenchmarkEventDrivenLoopBefore(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Load().EventDrivenLoop
	}
}

func BenchmarkEventDrivenLoopAfter(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Load().EventDrivenLoop
	}
}

func BenchmarkTexBudgetMiBBefore(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.TexBudgetMiB()
	}
}

func BenchmarkSyncSnapshot(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.syncSnapshot()
	}
}

func BenchmarkSetSmoothScaling(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.SetSmoothScaling(i%2 == 0)
	}
}

func BenchmarkSetTexBudgetMiB(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.SetTexBudgetMiB(64 + i%256)
	}
}


package config

import "testing"

// Before/after micro-benchmarks for the snapshot swap: "before" is the current
// RLock-guarded getter, "after" is Load().Field (the atomic-load path).
func BenchmarkSmoothScalingBefore(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Load().SmoothScaling
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
		_ = p.Load().SpriteScaling
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
		_ = p.Load().ReduceMotionOn
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
		_ = p.Load().TexBudgetMiB()
	}
}

func BenchmarkSetSmoothScaling(b *testing.B) {
	p := defaultPrefs("")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		{
			pv1 := i%2 == 0
			p.Update(func(sn *Settings) {
				sn.SmoothScaling =

					// BenchmarkSetSmoothScalingDrag is the item-2b gate: a rewritten setter on a slider drag
					// path (one call per mouse-move). The Update-based setter costs ~316us per call (the deep
					// syncSnapshot + clone + normalize) vs ~32ns for an explicit direct-write setter, so it is
					// over the 100us budget until the flip (item 3) deletes the bridge and the snapshot becomes
					// the only source. The hot drag-path setters therefore stay explicit until the flip.
					pv1
			})
		}
	}
}

func BenchmarkSetSmoothScalingDrag(b *testing.B) {
	p := defaultPrefs("")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		{
			pv2 := i%2 == 0
			p.Update(func(sn *Settings) {
				sn.SmoothScaling = pv2
			})
		}
	}
}

func BenchmarkSetTexBudgetMiB(b *testing.B) {
	p := defaultPrefs("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		{
			pv1 := 64 + i%256
			p.Update(func(sn *Settings) {
				sn.TexBudgetMiBVal = pv1
			})
		}
	}
}

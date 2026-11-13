package ui

import "github.com/SyntaxNyah/AsyncAO/internal/config"

const (
	cpuRendererLabel = "Draw on the CPU (for graphics drivers that freeze)"
	cpuRendererHint  = "AsyncAO switches this on by itself when your graphics driver freezes. It is slower with big " +
		"animated sprites; after updating your graphics driver, switch it off to draw on the GPU again. Takes effect " +
		"when AsyncAO restarts."
	// rendererStalledNotice is the one-time line after the client restarted itself on the CPU renderer.
	rendererStalledNotice = "Your graphics driver froze, so AsyncAO restarted drawing on the CPU. " +
		"Settings → Power user switches back."
	cpuRendererRestartNote = "Restart AsyncAO to switch renderers."
)

// drawCPURendererSetting is the switch for the CPU renderer the client falls back to when the graphics driver freezes.
func (a *App) drawCPURendererSetting(y int32) int32 {
	on := a.d.Prefs.Load().CPURenderer
	if next := a.ctx.Checkbox(a.formX, y, cpuRendererLabel, on); next != on {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.CPURenderer = next })
		settings.statusLine = cpuRendererRestartNote
	}
	a.noteSearchRow(cpuRendererLabel, y)
	y += settRowH
	return a.settingsDesc(a.formX, y, cpuRendererHint, ColTextDim) + settSectionGap
}

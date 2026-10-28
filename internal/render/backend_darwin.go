package render

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// macOS names no driver: SDL picks Metal, which the gpu renderer ships MSL
// shaders for. The UI library's SPIR-V shaders wait on plan open item 2.
const (
	gpuDriver        = ""
	gpuShaderFormats = sdl.GPUShaderFormatMSL
	gpuDriverLabel   = "metal"
)

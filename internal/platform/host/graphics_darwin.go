package host

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// macOS names no driver: SDL picks Metal, with MSL shaders; the UI library's SPIR-V shaders don't load there yet.
const (
	gpuDriver        = ""
	gpuShaderFormats = sdl.GPUShaderFormatMSL
	gpuDriverLabel   = "metal"
)

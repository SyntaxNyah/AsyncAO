//go:build !darwin

package host

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// Vulkan is named explicitly: SDL's own choice on Windows is D3D12, which
// rejects the SPIR-V-only shaders the UI library ships.
const (
	gpuDriver        = "vulkan"
	gpuShaderFormats = sdl.GPUShaderFormatSPIRV
	gpuDriverLabel   = gpuDriver
)

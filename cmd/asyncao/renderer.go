package main

import "fmt"

// The -renderer values.
const (
	rendererGPU = "gpu" // SDL_GPU: Vulkan, or Metal on macOS
	rendererSDL = "sdl" // SDL's own renderer: Direct3D 11 on Windows
)

// skipsGPUDevice reports whether -renderer asks for SDL's own renderer instead of SDL_GPU.
func skipsGPUDevice(renderer string) (bool, error) {
	switch renderer {
	case rendererGPU:
		return false, nil
	case rendererSDL:
		return true, nil
	}
	return false, fmt.Errorf("-renderer=%q: want %q or %q", renderer, rendererGPU, rendererSDL)
}

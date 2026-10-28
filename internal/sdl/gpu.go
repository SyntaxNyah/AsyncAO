package sdl

/*
#include <stdlib.h>
#include <SDL3/SDL.h>

static const char *asyncaoGPUDeviceName(SDL_GPUDevice *device) {
	return SDL_GetStringProperty(SDL_GetGPUDeviceProperties(device), SDL_PROP_GPU_DEVICE_NAME_STRING, NULL);
}
*/
import "C"

import "unsafe"

// GPUDevice is an SDL_GPU device. Renderers and, later, the UI library draw
// through one shared device.
type GPUDevice C.SDL_GPUDevice

// GPUShaderFormat names the shader formats an application can supply.
type GPUShaderFormat uint32

// Shader formats in use.
const (
	GPUShaderFormatSPIRV GPUShaderFormat = C.SDL_GPU_SHADERFORMAT_SPIRV
	GPUShaderFormatMSL   GPUShaderFormat = C.SDL_GPU_SHADERFORMAT_MSL
)

// CreateGPUDevice creates a device on the named driver ("vulkan", …), or on the
// driver SDL picks for formats when driver is empty.
func CreateGPUDevice(formats GPUShaderFormat, driver string) (*GPUDevice, error) {
	var cdriver *C.char
	if driver != "" {
		cdriver = C.CString(driver)
		defer C.free(unsafe.Pointer(cdriver))
	}
	d := C.SDL_CreateGPUDevice(C.SDL_GPUShaderFormat(formats), false, cdriver)
	if d == nil {
		return nil, errorOr(false)
	}
	return (*GPUDevice)(d), nil
}

func (d *GPUDevice) c() *C.SDL_GPUDevice { return (*C.SDL_GPUDevice)(d) }

// Destroy frees the device. Every renderer on it must be destroyed first.
func (d *GPUDevice) Destroy() { C.SDL_DestroyGPUDevice(d.c()) }

// Driver is the backend the device runs on ("vulkan", "metal", …).
func (d *GPUDevice) Driver() string { return C.GoString(C.SDL_GetGPUDeviceDriver(d.c())) }

// Name is the adapter the device runs on, such as the GPU's model or
// "llvmpipe" for the software driver, or "" when the driver doesn't say.
func (d *GPUDevice) Name() string {
	if n := C.asyncaoGPUDeviceName(d.c()); n != nil {
		return C.GoString(n)
	}
	return ""
}

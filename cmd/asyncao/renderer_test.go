package main

import "testing"

// TestSkipsGPUDevice: "sdl" skips SDL_GPU, "gpu" keeps it, and anything else is refused rather than guessed.
func TestSkipsGPUDevice(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    bool
		wantErr bool
	}{
		{rendererGPU, false, false},
		{rendererSDL, true, false},
		{"vulkan", false, true},
		{"", false, true},
	} {
		got, err := skipsGPUDevice(tc.value)
		if (err != nil) != tc.wantErr {
			t.Errorf("%q: error %v, want error %v", tc.value, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("%q: skips the device = %v, want %v", tc.value, got, tc.want)
		}
	}
}

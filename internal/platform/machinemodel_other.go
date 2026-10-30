//go:build !windows && !linux && !darwin

package platform

func machineModel() string { return "" }

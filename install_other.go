//go:build !linux && !darwin

package main

import (
	"fmt"
	"runtime"
)

func platformInstall() error {
	return fmt.Errorf("install is only supported on Linux (systemd) and macOS (launchd), not %s", runtime.GOOS)
}

func platformUninstall() error { return platformInstall() }

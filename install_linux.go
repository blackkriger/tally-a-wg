//go:build linux

package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed systemd/tallyawg.service
var unitFile string

//go:embed tallyawg.env.example
var envExample string

const (
	installEnv  = "/etc/tallyawg/tallyawg.env"
	installUnit = "/etc/systemd/system/tallyawg.service"
)

func platformInstall() error {
	if err := installSelf(); err != nil {
		return err
	}
	for _, d := range []string{filepath.Dir(installEnv), stateDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if _, err := os.Stat(installEnv); os.IsNotExist(err) {
		if err := os.WriteFile(installEnv, lf(envExample), 0o644); err != nil {
			return err
		}
		fmt.Println(">> wrote " + installEnv + " (edit it to set your flags)")
	} else {
		fmt.Println(">> kept existing " + installEnv)
	}
	if err := os.WriteFile(installUnit, lf(unitFile), 0o644); err != nil {
		return err
	}
	if err := run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := run("systemctl", "enable", "tallyawg.service"); err != nil {
		return err
	}
	// restart, not `enable --now`: on an upgrade the service is already running with the old binary
	if err := run("systemctl", "restart", "tallyawg.service"); err != nil {
		return err
	}
	fmt.Println(">> enabled tallyawg.service")
	return nil
}

func platformUninstall() error {
	_ = run("systemctl", "disable", "--now", "tallyawg.service")
	if err := os.Remove(installUnit); err == nil {
		fmt.Println(">> removed " + installUnit)
	}
	_ = run("systemctl", "daemon-reload")
	return nil
}

//go:build darwin

package main

import (
	_ "embed"
	"fmt"
	"os"
)

//go:embed launchd/tallyawg.plist
var plistFile string

const (
	launchLabel  = "com.github.blackkriger.tallyawg"
	installPlist = "/Library/LaunchDaemons/" + launchLabel + ".plist"
)

func platformInstall() error {
	if err := installSelf(); err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(installPlist); err == nil {
		fmt.Println(">> kept flags from existing " + installPlist)
	} else {
		if err := os.WriteFile(installPlist, lf(plistFile), 0o644); err != nil {
			return err
		}
		fmt.Println(">> wrote " + installPlist + " (edit ProgramArguments to set your flags)")
	}
	// bootout first so an upgrade reloads the new binary; ignore "not loaded"
	_ = run("launchctl", "bootout", "system/"+launchLabel)
	if err := run("launchctl", "bootstrap", "system", installPlist); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w", err)
	}
	fmt.Println(">> loaded " + launchLabel)
	return nil
}

func platformUninstall() error {
	_ = run("launchctl", "bootout", "system/"+launchLabel)
	if err := os.Remove(installPlist); err == nil {
		fmt.Println(">> removed " + installPlist)
	}
	return nil
}

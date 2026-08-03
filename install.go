package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// lf strips CR so a Windows checkout can't bake CRLF into the unit or the env file.
func lf(s string) []byte { return []byte(strings.ReplaceAll(s, "\r\n", "\n")) }

//go:embed systemd/tallyawg.service
var unitFile string

//go:embed tallyawg.env.example
var envExample string

const (
	installBin  = "/usr/local/bin/tallyawg"
	installEnv  = "/etc/tallyawg/tallyawg.env"
	installUnit = "/etc/systemd/system/tallyawg.service"
	stateDir    = "/var/lib/tallyawg"
)

func requireRootLinux(action string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("%s needs systemd; it only works on Linux", action)
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("run as root (sudo tallyawg %s)", action)
	}
	return nil
}

// copySelf writes the running binary to dst via a temp file + rename, so replacing a running copy works.
func copySelf(dst string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	src, err := os.Open(self)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func runInstall(args []string) {
	if err := requireRootLinux("install"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if same, _ := sameFileAsSelf(installBin); !same {
		if err := copySelf(installBin); err != nil {
			fmt.Fprintln(os.Stderr, "error: install binary:", err)
			os.Exit(1)
		}
		fmt.Println(">> installed " + installBin)
	} else {
		fmt.Println(">> already running from " + installBin)
	}

	for _, d := range []string{filepath.Dir(installEnv), stateDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	if _, err := os.Stat(installEnv); os.IsNotExist(err) {
		if err := os.WriteFile(installEnv, lf(envExample), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println(">> wrote " + installEnv + " (edit it to set your flags)")
	} else {
		fmt.Println(">> kept existing " + installEnv)
	}

	if err := os.WriteFile(installUnit, lf(unitFile), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for _, c := range [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "tallyawg.service"},
		// restart, not `enable --now`: on an upgrade the service is already running with the old binary
		{"systemctl", "restart", "tallyawg.service"},
	} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	fmt.Println(">> enabled tallyawg.service")
	fmt.Println()
	fmt.Println("done. report:    tallyawg report")
	fmt.Println("      web page:  http://127.0.0.1:8082  (put it behind your own reverse proxy + auth)")
}

func runUninstall(args []string) {
	if err := requireRootLinux("uninstall"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for _, c := range [][]string{
		{"systemctl", "disable", "--now", "tallyawg.service"},
		{"systemctl", "daemon-reload"},
	} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		_ = cmd.Run()
	}
	for _, f := range []string{installUnit, installBin} {
		if err := os.Remove(f); err == nil {
			fmt.Println(">> removed " + f)
		}
	}
	fmt.Println(">> kept " + installEnv + " and " + stateDir + " (ledger); remove them by hand if you want them gone")
}

func sameFileAsSelf(path string) (bool, error) {
	self, err := os.Executable()
	if err != nil {
		return false, err
	}
	a, err := os.Stat(self)
	if err != nil {
		return false, err
	}
	b, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return os.SameFile(a, b), nil
}

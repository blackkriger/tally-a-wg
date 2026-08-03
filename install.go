package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	installBin = "/usr/local/bin/tallyawg"
	stateDir   = "/var/lib/tallyawg"
)

// lf strips CR so a Windows checkout can't bake CRLF into the service files.
func lf(s string) []byte { return []byte(strings.ReplaceAll(s, "\r\n", "\n")) }

func requireRoot(action string) error {
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

func installSelf() error {
	if same, _ := sameFileAsSelf(installBin); same {
		fmt.Println(">> already running from " + installBin)
		return nil
	}
	if err := copySelf(installBin); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	fmt.Println(">> installed " + installBin)
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func runInstall(args []string) {
	if err := requireRoot("install"); err != nil {
		fail(err)
	}
	if err := platformInstall(); err != nil {
		fail(err)
	}
	fmt.Println()
	fmt.Println("done. report:    tallyawg report")
	fmt.Println("      web page:  http://127.0.0.1:8082  (put it behind your own reverse proxy + auth)")
}

func runUninstall(args []string) {
	if err := requireRoot("uninstall"); err != nil {
		fail(err)
	}
	if err := platformUninstall(); err != nil {
		fail(err)
	}
	if err := os.Remove(installBin); err == nil {
		fmt.Println(">> removed " + installBin)
	}
	fmt.Println(">> kept " + stateDir + " (ledger) and your config; remove them by hand if you want them gone")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

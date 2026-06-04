package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// wgBinary resolves the wg-tools command: an explicit one, else awg, else wg.
func wgBinary(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	for _, c := range []string{"awg", "wg"} {
		if _, err := exec.LookPath(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("neither 'awg' nor 'wg' found in PATH (set -wg)")
}

// backendName reports the protocol for display: "awg" or "wg".
func backendName(o *Options) string {
	wg, err := wgBinary(o.WG)
	if err != nil {
		return "wg"
	}
	if strings.Contains(filepath.Base(wg), "awg") {
		return "awg"
	}
	return "wg"
}

// wgInterface resolves the interface: explicit, else the first one that is up.
func wgInterface(wg, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	out, err := exec.Command(wg, "show", "interfaces").Output()
	if err != nil {
		return "", fmt.Errorf("%s show interfaces: %w", wg, err)
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return "", fmt.Errorf("no interface is up (set -i)")
	}
	return f[0], nil
}

// dumpPeer is one peer from `wg show <iface> dump`; rx/tx reset when the
// interface restarts.
type dumpPeer struct {
	pub, ip, endpoint string
	handshake         int64
	rx, tx            int64
}

func cleanIP(s string) string {
	if k := strings.IndexByte(s, '/'); k >= 0 {
		s = s[:k]
	}
	if c := strings.IndexByte(s, ','); c >= 0 {
		s = s[:c]
	}
	return strings.TrimSpace(s)
}

// readDump parses `wg show <iface> dump`. Line 0 is the interface; peer fields
// (tab-separated): pubkey, psk, endpoint, allowed-ips, handshake, rx, tx, keepalive.
func readDump(wg, iface string) ([]dumpPeer, error) {
	out, err := exec.Command(wg, "show", iface, "dump").Output()
	if err != nil {
		return nil, fmt.Errorf("%s show %s dump: %w", wg, iface, err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	peers := make([]dumpPeer, 0, len(lines))
	for i, line := range lines {
		if i == 0 || line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		hs, _ := strconv.ParseInt(f[4], 10, 64)
		rx, _ := strconv.ParseInt(f[5], 10, 64)
		tx, _ := strconv.ParseInt(f[6], 10, 64)
		ep := f[2]
		if ep == "(none)" {
			ep = ""
		}
		peers = append(peers, dumpPeer{
			pub: f[0], ip: cleanIP(f[3]), endpoint: ep,
			handshake: hs, rx: rx, tx: tx,
		})
	}
	return peers, nil
}

// liveDump returns the current dump keyed by pubkey, to overlay live status
// (handshake / session bytes / endpoint) onto the persistent rows.
func liveDump(o *Options) map[string]dumpPeer {
	m := map[string]dumpPeer{}
	wg, err := wgBinary(o.WG)
	if err != nil {
		return m
	}
	iface, err := wgInterface(wg, o.Interface)
	if err != nil {
		return m
	}
	peers, err := readDump(wg, iface)
	if err != nil {
		return m
	}
	for _, p := range peers {
		m[p.pub] = p
	}
	return m
}

// collectOnce takes one snapshot and folds it into the ledger on disk.
func collectOnce(o *Options) error {
	wg, err := wgBinary(o.WG)
	if err != nil {
		return err
	}
	iface, err := wgInterface(wg, o.Interface)
	if err != nil {
		return err
	}
	peers, err := readDump(wg, iface)
	if err != nil {
		return err
	}
	l, err := loadLedger(o.Data)
	if err != nil {
		return err
	}
	now := time.Now()
	l.maybeYearReset(now)
	for _, p := range peers {
		l.addDelta(now, p.pub, p.ip, p.rx, p.tx)
	}
	l.prune(now)
	return saveLedger(o.Data, l)
}

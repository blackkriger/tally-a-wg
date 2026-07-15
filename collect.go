package main

import (
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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

func backendLabel(o *Options) string {
	wg, err := wgBinary(o.WG)
	if err != nil {
		return "wg"
	}
	ifaces, err := wgInterfaces(wg, o.Interface)
	if err != nil || len(ifaces) == 0 {
		return backendName(o)
	}
	var hasWg, hasAwg bool
	for _, i := range ifaces {
		if ifaceKind(wg, i) == "awg" {
			hasAwg = true
		} else {
			hasWg = true
		}
	}
	switch {
	case hasWg && hasAwg:
		return "(a)wg"
	case hasAwg:
		return "awg"
	default:
		return "wg"
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func wgInterfaces(wg, explicit string) ([]string, error) {
	if explicit != "" {
		return splitList(explicit), nil
	}
	out, err := exec.Command(wg, "show", "interfaces").Output()
	if err != nil {
		return nil, fmt.Errorf("%s show interfaces: %w", wg, err)
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return nil, fmt.Errorf("no interface is up (set -i)")
	}
	return f, nil
}

// ifaceKind classifies an interface: "awg" if it reports junk params (jc/jmin), else "wg".
func ifaceKind(wg, iface string) string {
	out, err := exec.Command(wg, "show", iface).Output()
	if err != nil {
		return "wg"
	}
	if strings.Contains(string(out), "jc:") || strings.Contains(string(out), "jmin:") {
		return "awg"
	}
	return "wg"
}

// rx/tx reset when the interface restarts.
type dumpPeer struct {
	pub, ip, endpoint string
	kind              string
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

// dump fields (tab-separated): pubkey, psk, endpoint, allowed-ips, handshake, rx, tx, keepalive; line 0 is the interface.
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

func liveDump(o *Options) map[string]dumpPeer {
	m := map[string]dumpPeer{}
	wg, err := wgBinary(o.WG)
	if err != nil {
		return m
	}
	ifaces, err := wgInterfaces(wg, o.Interface)
	if err != nil {
		return m
	}
	for _, iface := range ifaces {
		peers, err := readDump(wg, iface)
		if err != nil {
			continue
		}
		kind := ifaceKind(wg, iface)
		for _, p := range peers {
			p.kind = kind
			m[p.pub] = p
		}
	}
	return m
}

func collectOnce(o *Options) error {
	wg, err := wgBinary(o.WG)
	if err != nil {
		return err
	}
	ifaces, err := wgInterfaces(wg, o.Interface)
	if err != nil {
		return err
	}
	l, err := loadLedger(o.Data)
	if err != nil {
		return err
	}
	now := time.Now()
	l.maybeYearReset(now)
	for _, iface := range ifaces {
		peers, err := readDump(wg, iface)
		if err != nil {
			log.Printf("skip interface %s: %v", iface, err)
			continue
		}
		for _, p := range peers {
			prevRaw, hadLast := l.Last[p.pub]
			l.addDelta(now, p.pub, p.ip, p.rx, p.tx)
			online := p.handshake > 0 && now.Unix()-p.handshake < onlineSecs
			l.updateSession(now, p.pub, p.rx, p.tx, online, prevRaw, hadLast)
		}
	}
	l.prune(now)
	return saveLedger(o.Data, l)
}

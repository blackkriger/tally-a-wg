package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// wgTimeout bounds every wg/awg call: a wedged one would otherwise hold the collector lock forever.
const wgTimeout = 10 * time.Second

func wgOutput(name string, args ...string) ([]byte, error) {
	return wgInput("", name, args...)
}

// wgInput runs a bounded wg/awg invocation, optionally feeding it stdin.
func wgInput(stdin, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wgTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.Output()
	// without this the caller only ever sees "exit status 1", never what the tool complained about
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
			return out, fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}

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

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// wgInterfaces returns the explicit -i list, else the union of what awg and wg report as up (they each only list their own interfaces).
func wgInterfaces(o *Options) ([]string, error) {
	if o.Interface != "" {
		return splitList(o.Interface), nil
	}
	bins := []string{o.WG}
	if o.WG == "" {
		bins = []string{"awg", "wg"}
	}
	seen := map[string]bool{}
	var out []string
	for _, b := range bins {
		if b == "" {
			continue
		}
		res, err := wgOutput(b, "show", "interfaces")
		if err != nil {
			continue
		}
		for _, f := range strings.Fields(string(res)) {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no interface is up (set -i)")
	}
	return out, nil
}

// ifaceDump reads one interface with the wg-tools binary that can handle it: the explicit -wg if set, otherwise awg then wg (awg can't read a plain wg iface).
func ifaceDump(o *Options, iface string) ([]dumpPeer, string, bool) {
	bins := []string{o.WG}
	if o.WG == "" {
		bins = []string{"awg", "wg"}
	}
	for _, b := range bins {
		if b == "" {
			continue
		}
		peers, err := readDump(b, iface)
		if err != nil {
			continue
		}
		return peers, ifaceKind(b, iface), true
	}
	return nil, "", false
}

// ifaceKind classifies an interface: "awg" if it reports junk params (jc/jmin), else "wg".
func ifaceKind(wg, iface string) string {
	out, err := wgOutput(wg, "show", iface)
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
	iface             string
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
	s = strings.TrimSpace(s)
	if s == "(none)" { // wg prints this for an empty AllowedIPs; it must not become the peer's name
		return ""
	}
	return s
}

// dump fields (tab-separated): pubkey, psk, endpoint, allowed-ips, handshake, rx, tx, keepalive; line 0 is the interface.
func readDump(wg, iface string) ([]dumpPeer, error) {
	out, err := wgOutput(wg, "show", iface, "dump")
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

// liveState returns the current per-peer dump plus the backend label in one pass over the interfaces (one dump + one kind probe each).
func liveState(o *Options) (map[string]dumpPeer, string) {
	m := map[string]dumpPeer{}
	ifaces, err := wgInterfaces(o)
	if err != nil {
		return m, backendName(o)
	}
	var hasWg, hasAwg bool
	for _, iface := range ifaces {
		peers, kind, ok := ifaceDump(o, iface)
		if !ok {
			continue
		}
		if kind == "awg" {
			hasAwg = true
		} else {
			hasWg = true
		}
		for _, p := range peers {
			if _, dup := m[p.pub]; dup { // first interface wins, matching how collectOnce accounts it
				continue
			}
			p.kind = kind
			p.iface = iface
			m[p.pub] = p
		}
	}
	switch {
	case hasWg && hasAwg:
		return m, "(a)wg"
	case hasAwg:
		return m, "awg"
	default:
		return m, "wg"
	}
}

func collectOnce(o *Options) error {
	ifaces, err := wgInterfaces(o)
	if err != nil {
		return err
	}
	return withLock(o.Data, func() error {
		l, err := loadLedger(o.Data)
		if err != nil {
			return err
		}
		now := time.Now()
		loc := parseZone(o.TZ)
		seen := map[string]bool{}
		for _, iface := range ifaces {
			peers, kind, ok := ifaceDump(o, iface)
			if !ok {
				log.Printf("skip interface %s: no wg-tools binary could read it", iface)
				continue
			}
			for _, p := range peers {
				if seen[p.pub] {
					log.Printf("skip duplicate peer %s on %s", p.pub, iface)
					continue
				}
				seen[p.pub] = true
				// the other interface's counters for this key are not comparable to these
				if q := l.Peers[p.pub]; q != nil && q.Iface != "" && q.Iface != iface {
					log.Printf("peer %s moved from %s to %s: restarting its baseline", p.pub, q.Iface, iface)
					delete(l.Last, p.pub)
				}
				prevRaw, hadLast := l.Last[p.pub]
				l.addDelta(now, loc, p.pub, p.ip, p.rx, p.tx)
				l.Peers[p.pub].Kind = kind
				l.Peers[p.pub].Iface = iface
				online := p.handshake > 0 && now.Unix()-p.handshake < onlineSecs
				l.updateSession(now, p.pub, p.rx, p.tx, online, p.handshake, prevRaw, hadLast)
			}
		}
		l.prune(now)
		return saveLedger(o.Data, l)
	})
}

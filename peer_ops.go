package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// addPeer keys a peer, claims an address, writes it to the config and the live interface.
func addPeer(o *Options, name, iface string) (string, error) {
	if !peerNameOK.MatchString(name) {
		return "", fmt.Errorf("peer names may use letters, digits, dot, dash and underscore (up to 32 chars)")
	}
	info, err := describeIface(o, iface)
	if err != nil {
		return "", err
	}
	// one writer at a time, or two adds hand out the same address and a delete loses one
	var conf string
	err = withLock(info.conf, func() (err error) {
		conf, err = createPeer(o, name, info)
		return
	})
	return conf, err
}

func createPeer(o *Options, name string, info *ifaceInfo) (string, error) {
	endpoint, err := endpointHost() // resolve before touching anything: a config nobody can dial is worse than no peer
	if err != nil {
		return "", err
	}
	byPub, _ := resolveNames(o)
	for _, existing := range byPub {
		if strings.EqualFold(existing, name) {
			return "", fmt.Errorf("a peer called %q already exists", name)
		}
	}
	if _, err := os.Stat(info.clientConfPath(name)); err == nil {
		return "", fmt.Errorf("a config for %q already exists", name)
	}

	addr, err := info.freeAddress()
	if err != nil {
		return "", err
	}
	priv, err := wgOutput(info.bin, "genkey")
	if err != nil {
		return "", fmt.Errorf("%s genkey: %w", info.bin, err)
	}
	privKey := strings.TrimSpace(string(priv))
	pubKey, err := pipeKey(info.bin, "pubkey", privKey)
	if err != nil {
		return "", err
	}
	psk, err := wgOutput(info.bin, "genpsk")
	if err != nil {
		return "", fmt.Errorf("%s genpsk: %w", info.bin, err)
	}
	pskKey := strings.TrimSpace(string(psk))

	block := fmt.Sprintf("\n[Peer]\n# %s\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = %s/32\n", name, pubKey, pskKey, addr)
	f, err := os.OpenFile(info.conf, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(block); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	// a half-created peer is live but its private key exists only here, so it is unusable
	undo := func() {
		_, _ = wgOutput(info.bin, "set", info.name, "peer", pubKey, "remove")
		_ = removePeerFromConf(info.conf, pubKey)
	}
	if err := applyPeer(info, pubKey, pskKey, addr); err != nil {
		undo()
		return "", err
	}

	conf := clientConfig(info, privKey, pskKey, addr, endpoint)
	if err := writeAtomic(info.clientConfPath(name), []byte(conf), 0o600); err != nil {
		undo()
		return "", err
	}
	writeQR(info.qrPath(name), conf) // best effort: qrencode may not be installed
	return conf, nil
}

// pipeKey feeds a key into `wg pubkey` and returns the result.
func pipeKey(bin, sub, in string) (string, error) {
	out, err := wgInput(in, bin, sub)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", bin, sub, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// the key goes on stdin: the AppArmor profile Ubuntu ships for wg refuses our temp files.
func applyPeer(i *ifaceInfo, pub, psk, addr string) error {
	_, err := wgInput(psk+"\n", i.bin, "set", i.name,
		"peer", pub, "preshared-key", "/dev/stdin", "allowed-ips", addr+"/32")
	if err != nil {
		return fmt.Errorf("%s set: %w", i.bin, err)
	}
	return nil
}

func clientConfig(i *ifaceInfo, priv, psk, addr, endpoint string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nAddress = %s/32\nDNS = 1.1.1.1, 1.0.0.1\n", priv, addr)
	for _, line := range i.junk {
		b.WriteString(line + "\n")
	}
	fmt.Fprintf(&b, "\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nEndpoint = %s:%s\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 25\n",
		i.srvPub, psk, endpoint, i.port)
	return b.String()
}

// we write the PNG ourselves: it holds the private key, and qrencode would use the umask.
func writeQR(path, conf string) {
	png, err := wgInput(conf, "qrencode", "-t", "png", "-o", "-")
	if err != nil {
		return
	}
	_ = writeAtomic(path, png, 0o600)
}

// ifaceOfKind resolves "wg" or "awg" to an interface of that flavour.
func ifaceOfKind(o *Options, kind string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "wg" && kind != "awg" {
		return "", fmt.Errorf("kind must be wg or awg")
	}
	ifaces, err := wgInterfaces(o)
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if info, err := describeIface(o, iface); err == nil && info.kind == kind {
			return iface, nil
		}
	}
	return "", fmt.Errorf("no %s interface is up", kind)
}

// peerFiles returns the client config and QR paths for a peer, if they exist on disk.
func peerFiles(o *Options, pub string) (conf, qr string, err error) {
	iface, err := ifaceHoldingPeer(o, pub)
	if err != nil {
		return "", "", err
	}
	info, err := describeIface(o, iface)
	if err != nil {
		return "", "", err
	}
	name := nameOfPeer(o, pub)
	if name != "" {
		if c := info.clientConfPath(name); fileExists(c) {
			return c, info.qrPath(name), nil
		}
	}
	// a peer filed under another name still had to be given the address the server hands it
	body, _ := os.ReadFile(info.conf)
	if c := clientConfWithAddress(info.dir, peerAddressIn(string(body), pub)); c != "" {
		return c, strings.TrimSuffix(c, ".conf") + ".png", nil
	}
	if name == "" {
		return "", "", fmt.Errorf("peer %s has no name, so no config was stored for it", pub)
	}
	return info.clientConfPath(name), info.qrPath(name), nil // report it as missing, not as unnamed
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// firstAddress drops the prefix length and any v6 address listed after the v4 one.
func firstAddress(v string) string {
	v, _, _ = strings.Cut(v, ",")
	v, _, _ = strings.Cut(v, "/")
	return strings.TrimSpace(v)
}

// peerAddressIn returns the address a server config gives pub through AllowedIPs.
func peerAddressIn(body, pub string) string {
	var block []string
	addrOfBlock := func() string {
		text := strings.Join(block, "\n")
		if !strings.Contains(text, pub) {
			return ""
		}
		return firstAddress(confValue(text, "AllowedIPs"))
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			if a := addrOfBlock(); a != "" {
				return a
			}
			block = nil
		}
		block = append(block, line)
	}
	return addrOfBlock()
}

// clientConfWithAddress finds the client config in dir written for addr, whatever its name.
func clientConfWithAddress(dir, addr string) string {
	if addr == "" {
		return ""
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.conf"))
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		text := string(body)
		if confValue(text, "ListenPort") != "" { // a server config living in the same directory
			continue
		}
		if firstAddress(confValue(text, "Address")) == addr {
			return p
		}
	}
	return ""
}

// removePeer erases the peer from the interface, the config, its files and the ledger.
func removePeer(o *Options, pub string) error {
	live, _ := liveState(o)
	d, ok := live[pub]
	iface := d.iface
	if !ok || iface == "" { // not running: fall back to whichever config mentions it
		var err error
		if iface, err = ifaceHoldingPeer(o, pub); err != nil {
			return err
		}
	}
	info, err := describeIface(o, iface)
	if err != nil {
		return err
	}
	return withLock(info.conf, func() error { // same writer lock the add side takes
		name := nameOfPeer(o, pub)
		if _, err := wgOutput(info.bin, "set", info.name, "peer", pub, "remove"); err != nil {
			return fmt.Errorf("%s set remove: %w", info.bin, err)
		}
		if err := removePeerFromConf(info.conf, pub); err != nil {
			return err
		}
		if name != "" {
			_ = os.Remove(info.clientConfPath(name))
			_ = os.Remove(info.qrPath(name))
		}
		return forgetPeer(o, pub)
	})
}

func ifaceHoldingPeer(o *Options, pub string) (string, error) {
	ifaces, err := wgInterfaces(o)
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		p := confPathFor(o, iface)
		if p == "" {
			continue
		}
		if body, err := os.ReadFile(p); err == nil && strings.Contains(string(body), pub) {
			return iface, nil
		}
	}
	return "", fmt.Errorf("no interface or config knows peer %s", pub)
}

func nameOfPeer(o *Options, pub string) string {
	byPub, _ := resolveNames(o)
	return byPub[pub]
}

// removePeerFromConf drops the [Peer] block holding pub, leaving the rest untouched.
func removePeerFromConf(path, pub string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")

	// find the block: a [Peer] header whose section mentions pub, up to the next header
	start, end := -1, len(lines)
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), "[peer]") {
			if start >= 0 { // the next peer closes the block we are dropping
				end = i
				break
			}
			for j := i + 1; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if strings.HasPrefix(t, "[") {
					break
				}
				if strings.Contains(t, pub) {
					start = i
					break
				}
			}
		} else if start >= 0 && strings.HasPrefix(strings.TrimSpace(l), "[") {
			end = i
			break
		}
	}
	if start < 0 {
		return nil // already gone
	}
	for start > 0 && strings.TrimSpace(lines[start-1]) == "" { // swallow the blank line above
		start--
	}
	kept := append(append([]string{}, lines[:start]...), lines[end:]...)
	return writeAtomic(path, []byte(strings.Join(kept, "\n")), 0o600)
}

// forgetPeer erases every trace of a peer from the ledger.
func forgetPeer(o *Options, pub string) error {
	return withLock(o.Data, func() error {
		l, err := loadLedger(o.Data)
		if err != nil {
			return err
		}
		delete(l.Peers, pub)
		delete(l.Last, pub)
		delete(l.SessBase, pub)
		delete(l.SessAt, pub)
		delete(l.PrevOn, pub)
		return saveLedger(o.Data, l)
	})
}

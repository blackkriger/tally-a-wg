package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Peer names end up in file names and config comments, so keep them boring.
var peerNameOK = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

type ifaceInfo struct {
	name    string // wg0
	kind    string // wg | awg
	bin     string // wg | awg
	conf    string // /etc/wireguard/wg0.conf
	dir     string // /etc/wireguard
	subnet  string // 10.9.0
	port    string
	srvPub  string
	junk    []string // awg obfuscation lines, copied verbatim into client configs
	present bool
}

// confCandidates lists where a server config for iface usually lives.
func confCandidates(iface string) []string {
	return []string{
		"/etc/wireguard/" + iface + ".conf",
		"/etc/amnezia/amneziawg/" + iface + ".conf",
		"/usr/local/etc/wireguard/" + iface + ".conf",
	}
}

func confPathFor(o *Options, iface string) string {
	for _, p := range splitList(o.Config) { // an explicit -config wins when it names this interface
		if strings.TrimSuffix(filepath.Base(p), ".conf") == iface {
			return p
		}
	}
	for _, p := range confCandidates(iface) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func confValue(body, key string) string {
	for _, line := range strings.Split(body, "\n") {
		f := strings.SplitN(line, "=", 2)
		if len(f) == 2 && strings.EqualFold(strings.TrimSpace(f[0]), key) {
			return strings.TrimSpace(f[1])
		}
	}
	return ""
}

// describeIface gathers everything needed to write a client config for iface.
func describeIface(o *Options, iface string) (*ifaceInfo, error) {
	info := &ifaceInfo{name: iface}
	bins := []string{o.WG}
	if o.WG == "" {
		bins = []string{"awg", "wg"}
	}
	for _, b := range bins {
		if b == "" {
			continue
		}
		if out, err := wgOutput(b, "show", iface, "public-key"); err == nil {
			info.bin = b
			info.srvPub = strings.TrimSpace(string(out))
			info.kind = ifaceKind(b, iface)
			break
		}
	}
	if info.bin == "" {
		return nil, fmt.Errorf("no wg-tools binary can read interface %s", iface)
	}

	info.conf = confPathFor(o, iface)
	if info.conf == "" {
		return nil, fmt.Errorf("cannot find the server config for %s (looked in %s)", iface, strings.Join(confCandidates(iface), ", "))
	}
	info.dir = filepath.Dir(info.conf)
	body, err := os.ReadFile(info.conf)
	if err != nil {
		return nil, err
	}
	text := string(body)

	addr := confValue(text, "Address")
	if i := strings.IndexByte(addr, ','); i >= 0 {
		addr = addr[:i]
	}
	ip, _, err := net.ParseCIDR(strings.TrimSpace(addr))
	if err != nil {
		return nil, fmt.Errorf("%s has no usable Address: %v", info.conf, err)
	}
	v4 := ip.To4()
	if v4 == nil {
		return nil, fmt.Errorf("%s uses an IPv6 address; peer creation needs an IPv4 subnet", info.conf)
	}
	info.subnet = fmt.Sprintf("%d.%d.%d", v4[0], v4[1], v4[2])

	info.port = confValue(text, "ListenPort")
	if info.port == "" {
		return nil, fmt.Errorf("%s has no ListenPort", info.conf)
	}
	for _, k := range []string{"Jc", "Jmin", "Jmax", "S1", "S2", "H1", "H2", "H3", "H4"} {
		if v := confValue(text, k); v != "" {
			info.junk = append(info.junk, k+" = "+v)
		}
	}
	info.present = true
	return info, nil
}

// freeAddress returns the lowest unused host in the interface's /24.
func (i *ifaceInfo) freeAddress() (string, error) {
	taken := map[int]bool{1: true} // .1 is the server
	out, err := wgOutput(i.bin, "show", i.name, "allowed-ips")
	if err == nil {
		for _, f := range strings.Fields(string(out)) {
			host, _, ok := strings.Cut(f, "/")
			if !ok || !strings.HasPrefix(host, i.subnet+".") {
				continue
			}
			if n, err := strconv.Atoi(host[len(i.subnet)+1:]); err == nil {
				taken[n] = true
			}
		}
	}
	for n := 2; n < 255; n++ {
		if !taken[n] {
			return fmt.Sprintf("%s.%d", i.subnet, n), nil
		}
	}
	return "", fmt.Errorf("no free address left in %s.0/24", i.subnet)
}

// endpointHost is the address clients dial: the source IP of the default route.
func endpointHost() (string, error) {
	c, err := net.Dial("udp", "1.1.1.1:80") // no packet is sent, this only resolves the route
	if err != nil {
		return "", fmt.Errorf("cannot work out this server's public address: %w", err)
	}
	defer c.Close()
	host, _, _ := net.SplitHostPort(c.LocalAddr().String())
	if host == "" {
		return "", fmt.Errorf("cannot work out this server's public address")
	}
	return host, nil
}

func (i *ifaceInfo) clientConfPath(name string) string {
	return filepath.Join(i.dir, "client_"+name+".conf")
}

func (i *ifaceInfo) qrPath(name string) string {
	return filepath.Join(i.dir, "client_"+name+".png")
}

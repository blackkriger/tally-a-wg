package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// parseAround also parses flags after the peer name, where flag.Parse would stop.
func parseAround(fs *flag.FlagSet, args []string) string {
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		return ""
	}
	name := fs.Arg(0)
	_ = fs.Parse(fs.Args()[1:])
	return name
}

func runPeer(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tallyawg peer add <name> [-kind wg|awg] | tallyawg peer rm <name>")
		os.Exit(2)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		peerAddCmd(rest)
	case "rm", "remove", "delete":
		peerRemoveCmd(rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown peer command %q\n", sub)
		os.Exit(2)
	}
}

func peerAddCmd(args []string) {
	o := &Options{}
	fs := newFlags("peer add", o)
	kind := fs.String("kind", "", "wg or awg (default: the only interface, or awg when both are up)")
	arg := parseAround(fs, args) // flags may sit before or after the peer name
	o.applyDefaults()

	name := strings.TrimSpace(arg)
	if name == "" {
		fail(fmt.Errorf("give the peer a name: tallyawg peer add <name>"))
	}
	if err := requireRoot("peer add"); err != nil {
		fail(err)
	}
	iface, err := pickIface(o, *kind)
	if err != nil {
		fail(err)
	}
	conf, err := addPeer(o, name, iface)
	if err != nil {
		fail(err)
	}
	fmt.Printf(">> added %s on %s\n\n%s", name, iface, conf)
}

func peerRemoveCmd(args []string) {
	o := &Options{}
	fs := newFlags("peer rm", o)
	arg := parseAround(fs, args) // flags may sit before or after the peer name
	o.applyDefaults()

	want := strings.TrimSpace(arg)
	if want == "" {
		fail(fmt.Errorf("give the peer to remove: tallyawg peer rm <name|pubkey>"))
	}
	if err := requireRoot("peer rm"); err != nil {
		fail(err)
	}
	pub, err := resolvePeer(o, want)
	if err != nil {
		fail(err)
	}
	if err := removePeer(o, pub); err != nil {
		fail(err)
	}
	fmt.Printf(">> removed %s along with its config and history\n", want)
}

// pickIface chooses the interface to create on: the requested kind, else the only one there is.
func pickIface(o *Options, kind string) (string, error) {
	if kind != "" {
		return ifaceOfKind(o, kind)
	}
	ifaces, err := wgInterfaces(o)
	if err != nil {
		return "", err
	}
	if len(ifaces) == 1 {
		return ifaces[0], nil
	}
	if iface, err := ifaceOfKind(o, "awg"); err == nil {
		return iface, nil
	}
	return ifaceOfKind(o, "wg")
}

// resolvePeer accepts a friendly name or a public key and returns the public key.
func resolvePeer(o *Options, want string) (string, error) {
	byPub, _ := resolveNames(o)
	if _, ok := byPub[want]; ok {
		return want, nil
	}
	var hits []string
	for pub, name := range byPub {
		if strings.EqualFold(name, want) {
			hits = append(hits, pub)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", fmt.Errorf("no peer called %q", want)
	default:
		return "", fmt.Errorf("%d peers are called %q; pass the public key instead", len(hits), want)
	}
}

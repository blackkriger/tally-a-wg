package main

import (
	"bufio"
	"os"
	"strings"
)

func namesFromConfig(path string) map[string]string {
	m := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	comment := ""
	for sc.Scan() {
		s := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(s, "#"):
			if c := strings.TrimSpace(strings.TrimLeft(s, "#")); c != "" {
				comment = c
			}
		case strings.EqualFold(s, "[peer]"):
			comment = ""
		case strings.HasPrefix(strings.ToLower(s), "publickey"):
			if comment != "" {
				if k := strings.IndexByte(s, '='); k >= 0 {
					m[strings.TrimSpace(s[k+1:])] = comment
				}
				comment = ""
			}
		}
	}
	return m
}

// namesFromFile reads "<pubkey-or-address> <name>" lines; keys with a dot/colon are addresses, the rest pubkeys.
func namesFromFile(path string) (byPub, byIP map[string]string) {
	byPub, byIP = map[string]string{}, map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		parts := strings.Fields(s)
		if len(parts) < 2 {
			continue
		}
		key, name := parts[0], strings.Join(parts[1:], " ")
		if strings.ContainsAny(key, ".:") {
			byIP[key] = name
		} else {
			byPub[key] = name
		}
	}
	return
}

// configPaths falls back to the up interfaces' configs, so names work with no flags.
func configPaths(o *Options) []string {
	if list := splitList(o.Config); len(list) > 0 {
		return list
	}
	ifaces, err := wgInterfaces(o)
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if p := confPathFor(o, iface); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func resolveNames(o *Options) (byPub, byIP map[string]string) {
	byPub, byIP = map[string]string{}, map[string]string{}
	for _, cfg := range configPaths(o) {
		for k, v := range namesFromConfig(cfg) {
			byPub[k] = v
		}
	}
	if o.Names != "" {
		p, i := namesFromFile(o.Names)
		for k, v := range p {
			byPub[k] = v
		}
		for k, v := range i {
			byIP[k] = v
		}
	}
	return
}

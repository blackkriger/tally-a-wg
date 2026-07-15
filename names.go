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

// namesFromFile reads "<pubkey-or-address> <name>" lines; keys >20 chars are pubkeys, the rest addresses.
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
		if len(key) > 20 {
			byPub[key] = name
		} else {
			byIP[key] = name
		}
	}
	return
}

func resolveNames(o *Options) (byPub, byIP map[string]string) {
	byPub, byIP = map[string]string{}, map[string]string{}
	for _, cfg := range splitList(o.Config) {
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

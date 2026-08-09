package main

import (
	"net"
	"net/http"
	"strings"
)

// never X-Forwarded-For: the client sets it, and this address is the whole gate.
func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(strings.TrimSpace(host))
}

func isAdmin(o *Options, r *http.Request) bool {
	ip := clientIP(r)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() { // only reachable from the box itself, or through an SSH tunnel
		return true
	}
	for _, a := range splitList(o.Admin) {
		if p := net.ParseIP(a); p != nil && p.Equal(ip) {
			return true
		}
	}
	return false
}

// requireAdmin gates every action that changes the server; it reports whether the caller may proceed.
func requireAdmin(w http.ResponseWriter, r *http.Request, o *Options) bool {
	if isAdmin(o, r) {
		return true
	}
	http.Error(w, "this address is not allowed to change anything: use a peer listed in -admin, or reach the page over an SSH tunnel", http.StatusForbidden)
	return false
}

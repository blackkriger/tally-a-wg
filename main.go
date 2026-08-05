// Command tallyawg keeps reset-safe per-peer traffic accounting for WireGuard/AmneziaWG.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-version") {
		printVersion()
		return
	}
	cmd := "report"
	if len(args) > 0 && !startsWithDash(args[0]) {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		runServe(args)
	case "collect":
		runCollect(args)
	case "report":
		runReport(args)
	case "install":
		runInstall(args)
	case "uninstall":
		runUninstall(args)
	case "version":
		printVersion()
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func startsWithDash(s string) bool { return len(s) > 0 && s[0] == '-' }

func usage() {
	fmt.Fprint(os.Stderr, `tally(a)wg - persistent per-peer traffic accounting for WireGuard / AmneziaWG

Usage:
  tallyawg serve     run the collector loop + web page
  tallyawg collect   take one snapshot into the ledger
  tallyawg report    print per-peer totals (default)
  tallyawg install   install as a service (Linux systemd / macOS launchd, root)
  tallyawg uninstall remove the service and the installed binary
  tallyawg version   print version

Common flags:
  -i, -interface  interface(s), comma-separated (default: all up)
  -wg             wg-tools binary (default: auto - awg, then wg)
  -config         server .conf(s), comma-separated, for "# name" comments
  -names          names file: "<pubkey-or-address> <name>" per line
  -data           ledger file (default: /var/lib/tallyawg/ledger.json)
  -tz             timezone for today/month (UTC, MSK, or an IANA name)

serve also accepts:
  -listen         address for the web page (default: 127.0.0.1:8082)
  -interval       collection interval (default: 5m)
`)
}

func runCollect(args []string) {
	o := &Options{}
	fs := newFlags("collect", o)
	_ = fs.Parse(args)
	o.applyDefaults()
	if err := collectOnce(o); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runReport(args []string) {
	o := &Options{}
	fs := newFlags("report", o)
	jsonOut := fs.Bool("json", false, "output JSON instead of a table")
	_ = fs.Parse(args)
	o.applyDefaults()
	l, err := loadLedger(o.Data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	byPub, byIP := resolveNames(o)
	loc := parseZone(o.TZ)
	now := time.Now()
	selMonth := now.In(loc).Format("2006-01")
	rows := l.rows(now, loc, selMonth, byPub, byIP)
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(rows)
		return
	}
	printTable(now, loc, selMonth, rows)
}

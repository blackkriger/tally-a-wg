package main

import "flag"

type Options struct {
	Interface string
	WG        string
	Config    string
	Names     string
	Data      string
	TZ        string
	Listen    string // serve only
}

const defaultData = "/var/lib/tallyawg/ledger.json"

func newFlags(name string, o *Options) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.StringVar(&o.Interface, "interface", "", "interface(s), comma-separated (default: all up)")
	fs.StringVar(&o.Interface, "i", "", "interface(s), comma-separated (shorthand)")
	fs.StringVar(&o.WG, "wg", "", "wg-tools binary: awg, wg, or blank to auto-detect")
	fs.StringVar(&o.Config, "config", "", `server .conf(s), comma-separated, for "# name" peer comments`)
	fs.StringVar(&o.Names, "names", "", `names file: "<pubkey-or-address> <name>" per line`)
	fs.StringVar(&o.Data, "data", defaultData, "ledger file")
	fs.StringVar(&o.TZ, "tz", "UTC", "timezone for today/month (UTC, an offset like +3, or an IANA name)")
	return fs
}

func (o *Options) applyDefaults() {
	if o.Data == "" {
		o.Data = defaultData
	}
}

package main

import "flag"

type Options struct {
	Interface string
	WG        string
	Config    string
	Names     string
	Data      string
	Listen    string // serve only
}

const defaultData = "/var/lib/tallyawg/ledger.json"

func newFlags(name string, o *Options) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.StringVar(&o.Interface, "interface", "", "interface name (default: auto-detect)")
	fs.StringVar(&o.Interface, "i", "", "interface name (shorthand)")
	fs.StringVar(&o.WG, "wg", "", "wg-tools binary: awg, wg, or blank to auto-detect")
	fs.StringVar(&o.Config, "config", "", `server .conf to read friendly "# name" peer comments from`)
	fs.StringVar(&o.Names, "names", "", `names file: "<pubkey-or-address> <name>" per line`)
	fs.StringVar(&o.Data, "data", defaultData, "ledger file")
	return fs
}

func (o *Options) applyDefaults() {
	if o.Data == "" {
		o.Data = defaultData
	}
}

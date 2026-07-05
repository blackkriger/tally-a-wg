package main

import (
	"fmt"
	"runtime"
)

// Stamped at build time via -ldflags "-X main.GitCommit=... -X main.BuildTime=...".
var (
	Version   = "0.2.0"
	GitCommit = "dev"
	BuildTime = "unknown"
)

func printVersion() {
	fmt.Printf("tallyawg %s (commit %s, built %s, %s)\n",
		Version, GitCommit, BuildTime, runtime.Version())
}

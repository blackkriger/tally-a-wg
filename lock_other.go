//go:build !unix

package main

// withLock is a no-op where flock is unavailable; ledger writes run unserialized.
func withLock(_ string, fn func() error) error { return fn() }

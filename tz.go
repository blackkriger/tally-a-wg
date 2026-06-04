package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embed the zoneinfo db so LoadLocation works without system tzdata
)

// parseZone accepts "" (UTC), a whole-hour offset ("+3", "-5"), or a name
// ("UTC", "MSK", "Europe/Berlin"); anything unrecognised falls back to UTC.
func parseZone(s string) *time.Location {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.UTC
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(s, "+")); err == nil {
		if n == 0 {
			return time.UTC
		}
		return time.FixedZone(fmt.Sprintf("UTC%+d", n), n*3600)
	}
	return loadLoc(s)
}

// loadLoc resolves a zone name; "MSK"/"Moscow" -> Europe/Moscow, else IANA, else UTC.
func loadLoc(name string) *time.Location {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "", "UTC", "GMT":
		return time.UTC
	case "MSK", "MOSCOW":
		name = "Europe/Moscow"
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.UTC
}

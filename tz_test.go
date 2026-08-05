package main

import (
	"testing"
	"time"
)

func TestParseZoneOffsets(t *testing.T) {
	for in, want := range map[string]int{
		"+3": 3 * 3600,
		"3":  3 * 3600,
		"-5": -5 * 3600,
		"0":  0,
		"":   0,
	} {
		_, off := time.Now().In(parseZone(in)).Zone()
		if off != want {
			t.Errorf("parseZone(%q) offset = %d, want %d", in, off, want)
		}
	}
}

func TestParseZoneNames(t *testing.T) {
	if parseZone("UTC") != time.UTC || parseZone("GMT") != time.UTC {
		t.Error("UTC/GMT should map to time.UTC")
	}
	if got := parseZone("MSK"); got.String() != "Europe/Moscow" {
		t.Errorf("MSK should map to Europe/Moscow, got %s", got)
	}
	if got := parseZone("Europe/Berlin"); got.String() != "Europe/Berlin" {
		t.Errorf("IANA name lost: %s", got)
	}
}

func TestParseZoneUnknownFallsBackToUTC(t *testing.T) {
	if parseZone("Not/AZone") != time.UTC {
		t.Error("an unknown zone must fall back to UTC")
	}
}

// "+0300" and friends parse as a plain number, so without a range check they shifted time by days.
func TestParseZoneRejectsOutOfRangeOffsets(t *testing.T) {
	for _, s := range []string{"+0300", "-0500", "15", "-13", "9223372036854775807"} {
		if got := parseZone(s); got != time.UTC {
			_, off := time.Now().In(got).Zone()
			t.Errorf("parseZone(%q) should fall back to UTC, got offset %d", s, off)
		}
	}
}

func TestRelAgoFormats(t *testing.T) {
	for in, want := range map[int64]string{
		-5:     "now",
		30:     "30s ago",
		90:     "1m ago",
		3600:   "1h 0m ago",
		7830:   "2h 10m ago",
		172800: "2d ago",
	} {
		if got := relAgo(in); got != want {
			t.Errorf("relAgo(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanUnits(t *testing.T) {
	for in, want := range map[int64]string{
		0:                  "0.0 B",
		512:                "512.0 B",
		1024:               "1.0 KiB",
		1024 * 1024:        "1.0 MiB",
		1024 * 1024 * 1024: "1.0 GiB",
	} {
		if got := human(in); got != want {
			t.Errorf("human(%d) = %q, want %q", in, got, want)
		}
	}
}

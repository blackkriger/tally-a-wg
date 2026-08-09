package main

import (
	"testing"
	"time"
)

// "today" must cover the local day exactly, including 23/25-hour DST days and :30 zones.
func TestTodayWindow(t *testing.T) {
	for _, tc := range []struct {
		zone  string
		day   string
		hours int
	}{
		{"UTC", "2026-08-09", 24},
		{"Europe/Moscow", "2026-08-09", 24},
		{"America/New_York", "2026-03-08", 23}, // clocks spring forward
		{"America/New_York", "2026-11-01", 25}, // clocks fall back
		{"Asia/Kolkata", "2026-08-09", 24},     // +05:30
		{"Australia/Eucla", "2026-08-09", 24},  // +08:45
	} {
		loc, err := time.LoadLocation(tc.zone)
		if err != nil {
			t.Skipf("no tzdata for %s: %v", tc.zone, err)
		}
		day, err := time.ParseInLocation("2006-01-02", tc.day, loc)
		if err != nil {
			t.Fatal(err)
		}
		next := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc)
		if got := int(next.Sub(day) / time.Hour); got != tc.hours {
			t.Fatalf("%s %s: the day is %d hours, the case says %d", tc.zone, tc.day, got, tc.hours)
		}

		// one unit of traffic in every UTC hour of a window wide enough to cover the day
		p := &Peer{Hours: map[string]*Bucket{}, Months: map[string]*Bucket{}}
		for h := day.Add(-48 * time.Hour); h.Before(next.Add(48 * time.Hour)); h = h.Add(time.Hour) {
			p.Hours[h.UTC().Format("2006-01-02T15")] = &Bucket{Rx: 1, Tx: 1}
		}
		l := &Ledger{Peers: map[string]*Peer{"k": p}}

		noon := day.Add(12 * time.Hour)
		rows := l.rows(noon, loc, noon.Format("2006-01"), nil, nil)
		if len(rows) != 1 {
			t.Fatalf("%s: %d rows", tc.zone, len(rows))
		}
		if int(rows[0].DownToday) != tc.hours {
			t.Errorf("%s %s: today covers %d hourly buckets, want %d",
				tc.zone, tc.day, rows[0].DownToday, tc.hours)
		}
	}
}

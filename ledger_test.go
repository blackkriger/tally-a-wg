package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const pub = "peerA"

var base = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// snap folds one raw counter reading into the ledger the way collectOnce does.
func snap(l *Ledger, now time.Time, rx, tx int64, hs int64) {
	prevRaw, hadLast := l.Last[pub]
	l.addDelta(now, pub, "10.0.0.2", rx, tx)
	online := hs > 0 && now.Unix()-hs < onlineSecs
	l.updateSession(now, pub, rx, tx, online, hs, prevRaw, hadLast)
}

func totals(l *Ledger) (rx, tx int64) {
	p := l.Peers[pub]
	if p == nil {
		return 0, 0
	}
	return p.Rx, p.Tx
}

func session(l *Ledger, rx, tx int64) (down, up int64) {
	b := l.SessBase[pub]
	return maxZero(tx - b[1]), maxZero(rx - b[0])
}

func TestAddDeltaFirstSightCountsWholeCounter(t *testing.T) {
	l := newLedger()
	snap(l, base, 100, 1000, base.Unix())
	if rx, tx := totals(l); rx != 100 || tx != 1000 {
		t.Fatalf("want 100/1000, got %d/%d", rx, tx)
	}
}

// Installing on a server that has been running for months must not book its whole history into today.
func TestAddDeltaFirstSightSkipsPeriodBuckets(t *testing.T) {
	l := newLedger()
	snap(l, base, 3_000_000, 9_000_000, base.Unix())

	p := l.Peers[pub]
	if len(p.Hours) != 0 || len(p.Months) != 0 {
		t.Fatalf("first sight must not fill today/month, got hours=%v months=%v", p.Hours, p.Months)
	}
	if rx, tx := totals(l); rx != 3_000_000 || tx != 9_000_000 {
		t.Fatalf("lifetime total should still count it, got %d/%d", rx, tx)
	}

	snap(l, base, 3_000_100, 9_001_000, base.Unix())
	if r := l.rows(base, time.UTC, "2026-08", nil, nil)[0]; r.DownToday != 1000 || r.UpToday != 100 {
		t.Fatalf("later readings must be bucketed, got today %d/%d", r.DownToday, r.UpToday)
	}
}

func TestAddDeltaAccumulatesGrowth(t *testing.T) {
	l := newLedger()
	snap(l, base, 100, 1000, base.Unix())
	snap(l, base, 150, 2500, base.Unix())
	if rx, tx := totals(l); rx != 150 || tx != 2500 {
		t.Fatalf("want 150/2500, got %d/%d", rx, tx)
	}
}

// A counter below its previous value means the interface restarted: the new value is the delta.
func TestAddDeltaSurvivesCounterReset(t *testing.T) {
	l := newLedger()
	snap(l, base, 500, 5000, base.Unix())
	snap(l, base, 30, 300, base.Unix())
	if rx, tx := totals(l); rx != 530 || tx != 5300 {
		t.Fatalf("want 530/5300, got %d/%d", rx, tx)
	}
}

func TestSessionStartsAtHandshakeNotCollectorTick(t *testing.T) {
	hs := base.Add(-43 * time.Second).Unix()
	l := newLedger()
	snap(l, base, 100, 1000, hs)
	if l.SessAt[pub] != hs {
		t.Fatalf("session start %d, want handshake %d", l.SessAt[pub], hs)
	}
}

// A peer that never handshaked must not get a session stamp at all.
func TestSessionNotStampedForNeverConnectedPeer(t *testing.T) {
	l := newLedger()
	snap(l, base, 0, 0, 0)
	if at, ok := l.SessAt[pub]; ok && at != 0 {
		t.Fatalf("offline peer got session start %d", at)
	}
}

func TestSessionCountsOnlyCurrentConnection(t *testing.T) {
	l := newLedger()
	hs := base.Unix()
	snap(l, base, 100, 1000, hs)
	snap(l, base, 150, 2000, hs)
	if d, u := session(l, 150, 2000); d != 1000 || u != 50 {
		t.Fatalf("want down 1000 / up 50, got %d/%d", d, u)
	}
}

// Going offline freezes the session; reconnecting rebaselines it from the previous reading.
func TestSessionRebaselinesOnReconnect(t *testing.T) {
	l := newLedger()
	old := base.Add(-2 * time.Hour)
	snap(l, old, 100, 1000, old.Unix())
	snap(l, old.Add(time.Minute), 150, 2000, old.Unix())

	stale := base.Add(-30 * time.Minute).Unix()
	snap(l, base, 150, 2000, stale)
	if d, u := session(l, 150, 2000); d != 1000 || u != 50 {
		t.Fatalf("offline session should stay 1000/50, got %d/%d", d, u)
	}

	snap(l, base.Add(time.Minute), 160, 2100, base.Add(time.Minute).Unix())
	if d, u := session(l, 160, 2100); d != 100 || u != 10 {
		t.Fatalf("new session want 100/10, got %d/%d", d, u)
	}
}

func TestSessionRestartsWhenCounterDropsBelowBaseline(t *testing.T) {
	l := newLedger()
	hs := base.Unix()
	snap(l, base, 500, 5000, hs)
	snap(l, base, 600, 6000, hs)
	snap(l, base, 20, 200, hs)
	if d, u := session(l, 20, 200); d != 200 || u != 20 {
		t.Fatalf("want the post-restart counters 200/20, got %d/%d", d, u)
	}
}

func TestRowsTodayHonoursTimezone(t *testing.T) {
	l := newLedger()
	// 23:30 UTC on Aug 3 is already Aug 4 in UTC+3
	late := time.Date(2026, 8, 3, 23, 30, 0, 0, time.UTC)
	snap(l, late, 0, 0, late.Unix()) // first sight only sets the baseline
	snap(l, late, 100, 1000, late.Unix())
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)

	utc := l.rows(now, time.UTC, "2026-08", nil, nil)
	if utc[0].DownToday != 0 {
		t.Fatalf("in UTC that hour is yesterday, want 0 today, got %d", utc[0].DownToday)
	}
	east := l.rows(now, time.FixedZone("UTC+3", 3*3600), "2026-08", nil, nil)
	if east[0].DownToday != 1000 {
		t.Fatalf("in UTC+3 that hour is today, want 1000, got %d", east[0].DownToday)
	}
}

func TestRowsMonthSelection(t *testing.T) {
	l := newLedger()
	july := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	snap(l, july, 0, 0, july.Unix()) // first sight only sets the baseline
	snap(l, july, 100, 1000, july.Unix())
	snap(l, base, 200, 3000, base.Unix())

	rows := l.rows(base, time.UTC, "2026-07", nil, nil)
	if rows[0].DownMonth != 1000 {
		t.Fatalf("july want 1000, got %d", rows[0].DownMonth)
	}
	rows = l.rows(base, time.UTC, "2026-08", nil, nil)
	if rows[0].DownMonth != 2000 {
		t.Fatalf("august want 2000, got %d", rows[0].DownMonth)
	}
}

func TestRowsNamePreference(t *testing.T) {
	l := newLedger()
	snap(l, base, 10, 10, base.Unix())

	if got := l.rows(base, time.UTC, "2026-08", map[string]string{pub: "from-config"}, map[string]string{"10.0.0.2": "from-names"})[0].Peer; got != "from-config" {
		t.Fatalf("pubkey name should win, got %q", got)
	}
	if got := l.rows(base, time.UTC, "2026-08", nil, map[string]string{"10.0.0.2": "from-names"})[0].Peer; got != "from-names" {
		t.Fatalf("address name should be used, got %q", got)
	}
	if got := l.rows(base, time.UTC, "2026-08", nil, nil)[0].Peer; got != "10.0.0.2" {
		t.Fatalf("address should be the fallback, got %q", got)
	}
}

func TestPruneDropsHoursOlderThanRetention(t *testing.T) {
	l := newLedger()
	old := base.Add(-(keepHours + 24) * time.Hour)
	snap(l, old, 0, 0, old.Unix()) // first sight only sets the baseline
	snap(l, old, 100, 1000, old.Unix())
	snap(l, base, 200, 2000, base.Unix())
	l.prune(base)

	p := l.Peers[pub]
	if len(p.Hours) != 1 {
		t.Fatalf("want only the recent hour left, got %d", len(p.Hours))
	}
	if p.Months[old.Format("2006-01")] == nil {
		t.Fatal("monthly history must survive pruning")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	l := newLedger()
	snap(l, base, 100, 1000, base.Unix())
	if err := saveLedger(path, l); err != nil {
		t.Fatal(err)
	}
	got, err := loadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if rx, tx := totals(got); rx != 100 || tx != 1000 {
		t.Fatalf("want 100/1000 after reload, got %d/%d", rx, tx)
	}
	if got.Last[pub] != [2]int64{100, 1000} {
		t.Fatalf("raw counters lost: %v", got.Last[pub])
	}
}

// A truncated hour key used to panic the backfill and put the collector in a restart loop.
func TestLoadSurvivesShortHourKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	body := `{"peers":{"K":{"ip":"10.0.0.2","rx":5,"tx":7,"hours":{"2026":{"rx":1,"tx":1}},"months":{}}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := loadLedger(path)
	if err != nil {
		t.Fatalf("want a usable ledger, got %v", err)
	}
	if l.Peers["K"].Tx != 7 {
		t.Fatalf("peer totals lost: %+v", l.Peers["K"])
	}
}

func TestLoadMissingLedgerStartsEmpty(t *testing.T) {
	l, err := loadLedger(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("a missing ledger is not an error: %v", err)
	}
	if len(l.Peers) != 0 {
		t.Fatalf("want an empty ledger, got %d peers", len(l.Peers))
	}
}

func TestSaveLedgerLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	if err := saveLedger(path, newLedger()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file should be renamed away")
	}
}

// TOTAL is lifetime: a year rollover keeps accumulating instead of wiping.
func TestTotalSurvivesYearRollover(t *testing.T) {
	l := newLedger()
	dec := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)
	snap(l, dec, 0, 0, dec.Unix()) // first sight only sets the baseline
	snap(l, dec, 100, 1000, dec.Unix())
	jan := time.Date(2027, 1, 1, 0, 30, 0, 0, time.UTC)
	snap(l, jan, 160, 1500, jan.Unix())

	if rx, tx := totals(l); rx != 160 || tx != 1500 {
		t.Fatalf("want 160/1500 lifetime, got %d/%d", rx, tx)
	}
	if p := l.Peers[pub]; p.Months["2026-12"] == nil || p.Months["2027-01"] == nil {
		t.Fatalf("both years must stay in the ledger, got %v", p.Months)
	}
}

func TestRowsYearSumsOnlyItsOwnMonths(t *testing.T) {
	l := newLedger()
	nov := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	snap(l, nov, 0, 0, 0) // first sight only sets the baseline
	snap(l, nov, 100, 1000, 0)
	snap(l, time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC), 200, 3000, 0)
	snap(l, time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC), 250, 3500, 0)
	now := time.Date(2027, 2, 1, 12, 0, 0, 0, time.UTC)

	r2026 := l.rows(now, time.UTC, "2026-12", nil, nil)[0]
	if r2026.DownYear != 3000 || r2026.UpYear != 200 {
		t.Fatalf("2026 want down 3000 / up 200, got %d/%d", r2026.DownYear, r2026.UpYear)
	}
	r2027 := l.rows(now, time.UTC, "2027-01", nil, nil)[0]
	if r2027.DownYear != 500 || r2027.UpYear != 50 {
		t.Fatalf("2027 want down 500 / up 50, got %d/%d", r2027.DownYear, r2027.UpYear)
	}
	if r2027.DownTotal != 3500 {
		t.Fatalf("total stays lifetime, want 3500, got %d", r2027.DownTotal)
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// keepHours bounds per-peer hourly detail (~70 days) so "today" can be
// recomputed in any whole-hour timezone; month and total are kept separately.
const keepHours = 70 * 24

// Bucket is a directional byte count. Rx = peer upload (server received),
// Tx = peer download (server sent).
type Bucket struct {
	Rx int64 `json:"rx"`
	Tx int64 `json:"tx"`
}

// Peer is one peer's accumulated usage. Hours keyed by UTC "2006-01-02T15",
// Months by UTC "2006-01".
type Peer struct {
	IP     string             `json:"ip"`
	Rx     int64              `json:"rx"`
	Tx     int64              `json:"tx"`
	Hours  map[string]*Bucket `json:"hours"`
	Months map[string]*Bucket `json:"months"`
}

// Ledger is the whole persistent state. Year is the UTC year; a rollover wipes
// the accumulators so each year starts from zero.
type Ledger struct {
	Year     int                 `json:"year"`
	Peers    map[string]*Peer    `json:"peers"`       // keyed by peer public key
	Last     map[string][2]int64 `json:"last"`        // pubkey -> last raw [rx, tx]
	SessBase map[string][2]int64 `json:"sess_base"`   // pubkey -> raw [rx, tx] at session start
	SessAt   map[string]int64    `json:"sess_at"`     // pubkey -> session start (unix)
	PrevOn   map[string]bool     `json:"prev_online"` // pubkey -> online at previous snapshot
}

func newLedger() *Ledger {
	return &Ledger{
		Peers:    map[string]*Peer{},
		Last:     map[string][2]int64{},
		SessBase: map[string][2]int64{},
		SessAt:   map[string]int64{},
		PrevOn:   map[string]bool{},
	}
}

func loadLedger(path string) (*Ledger, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newLedger(), nil
		}
		return nil, err
	}
	l := newLedger()
	if err := json.Unmarshal(b, l); err != nil {
		return nil, err
	}
	if l.Peers == nil {
		l.Peers = map[string]*Peer{}
	}
	if l.Last == nil {
		l.Last = map[string][2]int64{}
	}
	if l.SessBase == nil {
		l.SessBase = map[string][2]int64{}
	}
	if l.SessAt == nil {
		l.SessAt = map[string]int64{}
	}
	if l.PrevOn == nil {
		l.PrevOn = map[string]bool{}
	}
	for _, p := range l.Peers {
		if p.Hours == nil {
			p.Hours = map[string]*Bucket{}
		}
		if p.Months == nil {
			p.Months = map[string]*Bucket{}
		}
		// backfill months from hours on first upgrade
		if len(p.Months) == 0 && len(p.Hours) > 0 {
			for hk, b := range p.Hours {
				addBucket(p.Months, hk[:7], b.Rx, b.Tx) // "2006-01"
			}
		}
	}
	return l, nil
}

func saveLedger(path string, l *Ledger) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic replace
}

// maybeYearReset zeroes the accumulators on a UTC year rollover. Last (raw
// counters) is kept so the delta math stays correct across the boundary.
func (l *Ledger) maybeYearReset(now time.Time) {
	y := now.UTC().Year()
	if l.Year == 0 {
		l.Year = y
		return
	}
	if y > l.Year {
		for _, p := range l.Peers {
			p.Rx, p.Tx = 0, 0
			p.Hours = map[string]*Bucket{}
			p.Months = map[string]*Bucket{}
		}
		l.Year = y
	}
}

// addDelta folds raw counters into the ledger, reset-safe: a counter below its
// last value means a restart, so the current value is taken as the delta.
func (l *Ledger) addDelta(now time.Time, pub, ip string, rx, tx int64) {
	var drx, dtx int64
	if last, seen := l.Last[pub]; !seen {
		drx, dtx = rx, tx // first sight
	} else {
		if rx >= last[0] {
			drx = rx - last[0]
		} else {
			drx = rx
		}
		if tx >= last[1] {
			dtx = tx - last[1]
		} else {
			dtx = tx
		}
	}

	p := l.Peers[pub]
	if p == nil {
		p = &Peer{Hours: map[string]*Bucket{}, Months: map[string]*Bucket{}}
		l.Peers[pub] = p
	}
	p.IP = ip
	p.Rx += drx
	p.Tx += dtx
	addBucket(p.Hours, now.UTC().Format("2006-01-02T15"), drx, dtx)
	addBucket(p.Months, now.UTC().Format("2006-01"), drx, dtx)
	l.Last[pub] = [2]int64{rx, tx}
}

// onlineSecs is the handshake-age cutoff (seconds) for "online".
const onlineSecs = 180

// updateSession rebaselines a peer's session counters on each offline->online transition.
func (l *Ledger) updateSession(now time.Time, pub string, rx, tx int64, online bool, prevRaw [2]int64, hadLast bool) {
	base, hasBase := l.SessBase[pub]
	restart := hasBase && (rx < base[0] || tx < base[1])
	if !hasBase || restart || (online && !l.PrevOn[pub]) {
		if hadLast && !restart {
			l.SessBase[pub] = prevRaw
		} else {
			l.SessBase[pub] = [2]int64{rx, tx}
		}
		l.SessAt[pub] = now.Unix()
	}
	l.PrevOn[pub] = online
}

func addBucket(m map[string]*Bucket, key string, rx, tx int64) {
	b := m[key]
	if b == nil {
		b = &Bucket{}
		m[key] = b
	}
	b.Rx += rx
	b.Tx += tx
}

// prune drops hourly buckets older than keepHours; monthly buckets are kept.
func (l *Ledger) prune(now time.Time) {
	cutoff := now.UTC().Add(-keepHours * time.Hour).Format("2006-01-02T15")
	for _, p := range l.Peers {
		for h := range p.Hours {
			if h < cutoff {
				delete(p.Hours, h)
			}
		}
	}
}

// availableMonths returns the sorted months that hold any data.
func (l *Ledger) availableMonths() []string {
	set := map[string]struct{}{}
	for _, p := range l.Peers {
		for mk, b := range p.Months {
			if b.Rx+b.Tx > 0 {
				set[mk] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for mk := range set {
		out = append(out, mk)
	}
	sort.Strings(out)
	return out
}

// Row is a flattened per-peer view for the report and the web API.
type Row struct {
	Peer      string `json:"peer"`
	IP        string `json:"ip"`
	Pubkey    string `json:"pubkey"`
	DownTotal int64  `json:"down_total"`
	UpTotal   int64  `json:"up_total"`
	DownMonth int64  `json:"down_month"`
	UpMonth   int64  `json:"up_month"`
	DownToday int64  `json:"down_today"`
	UpToday   int64  `json:"up_today"`
	DownWin   int64  `json:"down_window"`
	UpWin     int64  `json:"up_window"`
}

// rows flattens the ledger: today/window from hourly buckets (today honours
// loc's day boundary), month from selMonth, total is lifetime.
func (l *Ledger) rows(now time.Time, loc *time.Location, windowStart time.Time, selMonth string, byPub, byIP map[string]string) []Row {
	curDay := now.In(loc).Format("2006-01-02")
	winKey := windowStart.UTC().Format("2006-01-02T15")
	rows := make([]Row, 0, len(l.Peers))
	for pub, p := range l.Peers {
		name := byPub[pub]
		if name == "" {
			name = byIP[p.IP]
		}
		if name == "" {
			if p.IP != "" {
				name = p.IP
			} else if len(pub) >= 12 {
				name = pub[:12]
			} else {
				name = pub
			}
		}
		var dRx, dTx, wRx, wTx int64
		for hk, b := range p.Hours {
			ht, err := time.ParseInLocation("2006-01-02T15", hk, time.UTC)
			if err != nil {
				continue
			}
			if ht.In(loc).Format("2006-01-02") == curDay {
				dRx += b.Rx
				dTx += b.Tx
			}
			if hk >= winKey {
				wRx += b.Rx
				wTx += b.Tx
			}
		}
		var mRx, mTx int64
		if b := p.Months[selMonth]; b != nil {
			mRx, mTx = b.Rx, b.Tx
		}
		// down = peer download = server Tx; up = peer upload = server Rx.
		rows = append(rows, Row{
			Peer: name, IP: p.IP, Pubkey: pub,
			DownTotal: p.Tx, UpTotal: p.Rx,
			DownMonth: mTx, UpMonth: mRx,
			DownToday: dTx, UpToday: dRx,
			DownWin: wTx, UpWin: wRx,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].DownTotal+rows[i].UpTotal > rows[j].DownTotal+rows[j].UpTotal
	})
	return rows
}

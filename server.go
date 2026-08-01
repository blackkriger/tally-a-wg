package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"
)

//go:embed static
var staticEmbed embed.FS

type apiPeer struct {
	Row
	Online      bool   `json:"online"`
	Handshake   string `json:"handshake"`
	SessionDown int64  `json:"session_down"`
	SessionUp   int64  `json:"session_up"`
	SessStart   int64  `json:"session_start"`
	HandshakeAt int64  `json:"handshake_at"`
	Endpoint    string `json:"endpoint"`
	Kind        string `json:"kind"`
}

func runServe(args []string) {
	o := &Options{}
	fset := newFlags("serve", o)
	fset.StringVar(&o.Listen, "listen", "127.0.0.1:8082", "address for the web page")
	interval := fset.Duration("interval", 5*time.Minute, "collection interval")
	_ = fset.Parse(args)
	o.applyDefaults()

	var mu sync.Mutex
	collect := func() {
		mu.Lock()
		defer mu.Unlock()
		if err := collectOnce(o); err != nil {
			log.Printf("collect error: %v", err)
		}
	}
	trigger := make(chan struct{}, 1) // debounced kick for a new peer seen via the API
	collect()                         // immediate first snapshot
	go func() {
		t := time.NewTicker(*interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
			case <-trigger:
			}
			collect()
		}
	}()

	sub, err := fs.Sub(staticEmbed, "static")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/usage", func(w http.ResponseWriter, r *http.Request) {
		loc := parseZone(r.URL.Query().Get("tz"))
		mu.Lock()
		l, lerr := loadLedger(o.Data)
		mu.Unlock()
		if lerr != nil {
			http.Error(w, lerr.Error(), http.StatusInternalServerError)
			return
		}
		byPub, byIP := resolveNames(o)
		now := time.Now()
		selMonth := r.URL.Query().Get("month")
		if selMonth == "" {
			selMonth = now.UTC().Format("2006-01")
		}
		rows := l.rows(now, loc, selMonth, byPub, byIP)
		live, backend := liveState(o)

		// also surface live peers not in the ledger yet (freshly created; they enter it on the next collect)
		seen := make(map[string]bool, len(rows))
		for _, r := range rows {
			seen[r.Pubkey] = true
		}
		hasNew := false
		for pub, d := range live {
			if seen[pub] {
				continue
			}
			hasNew = true
			name := byPub[pub]
			if name == "" {
				name = byIP[d.ip]
			}
			if name == "" {
				if d.ip != "" {
					name = d.ip
				} else if len(pub) >= 12 {
					name = pub[:12]
				} else {
					name = pub
				}
			}
			rows = append(rows, Row{Peer: name, IP: d.ip, Pubkey: pub})
		}
		if hasNew { // kick an off-cycle collect so the new peer lands in the ledger now
			select {
			case trigger <- struct{}{}:
			default:
			}
		}

		peers := make([]apiPeer, 0, len(rows))
		for _, row := range rows {
			ap := apiPeer{Row: row, Handshake: "never"}
			if d, ok := live[row.Pubkey]; ok {
				ap.Endpoint = d.endpoint
				ap.Kind = d.kind
				ap.SessStart = l.SessAt[row.Pubkey]
				ap.HandshakeAt = d.handshake
				// session = live counter since the current session's baseline
				if base, ok := l.SessBase[row.Pubkey]; ok {
					ap.SessionDown = maxZero(d.tx - base[1])
					ap.SessionUp = maxZero(d.rx - base[0])
				} else {
					ap.SessionDown = d.tx
					ap.SessionUp = d.rx
				}
				if d.handshake > 0 {
					ago := now.Unix() - d.handshake
					ap.Online = ago < onlineSecs
					ap.Handshake = relAgo(ago)
				}
			}
			peers = append(peers, ap)
		}

		tnow := now.In(loc)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"now":     tnow.Format("2006-01-02 15:04 MST"),
			"month":   selMonth,
			"months":  l.availableMonths(),
			"tz":      tnow.Format("MST"),
			"backend": backend,
			"peers":   peers,
		})
	})

	log.Printf("tallyawg serving on http://%s (interface=%s, data=%s)", o.Listen, o.Interface, o.Data)
	log.Fatal(http.ListenAndServe(o.Listen, mux))
}

func maxZero(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

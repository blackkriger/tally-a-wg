package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"
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
	collect() // immediate first snapshot
	go func() {
		t := time.NewTicker(*interval)
		defer t.Stop()
		for range t.C {
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
		hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
		if hours <= 0 {
			hours = 24
		}
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
		rows := l.rows(now, loc, now.Add(-time.Duration(hours)*time.Hour), selMonth, byPub, byIP)
		live := liveDump(o)

		peers := make([]apiPeer, 0, len(rows))
		for _, row := range rows {
			ap := apiPeer{Row: row, Handshake: "never"}
			if d, ok := live[row.Pubkey]; ok {
				ap.Endpoint = d.endpoint
				ap.Kind = d.kind
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
			"now":          tnow.Format("2006-01-02 15:04 MST"),
			"month":        selMonth,
			"months":       l.availableMonths(),
			"tz":           tnow.Format("MST"),
			"window_hours": hours,
			"backend":      backendLabel(o),
			"peers":        peers,
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

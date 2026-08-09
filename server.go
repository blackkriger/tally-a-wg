package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
}

func runServe(args []string) {
	o := &Options{}
	fset := newFlags("serve", o)
	fset.StringVar(&o.Listen, "listen", "127.0.0.1:8082", "address for the web page")
	fset.StringVar(&o.Admin, "admin", "", "tunnel address(es) allowed to add/remove peers, comma-separated (the loopback always is)")
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
		tz := r.URL.Query().Get("tz")
		if tz == "" {
			tz = o.TZ // the -tz flag is the default for clients that don't ask for one
		}
		loc := parseZone(tz)
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
			// months are bucketed in the collector's zone, so the default must come from it
			selMonth = now.In(parseZone(o.TZ)).Format("2006-01")
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
			"version": Version,
			"peers":   peers,
		})
	})

	// POST /api/peers creates; DELETE and /config, /qr act on /api/peers/<pubkey>
	mux.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST to create a peer", http.StatusMethodNotAllowed)
			return
		}
		if !requireAdmin(w, r, o) {
			return
		}
		var req struct{ Name, Kind string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		iface, err := ifaceOfKind(o, req.Kind)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		conf, err := addPeer(o, strings.TrimSpace(req.Name), iface)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": req.Name, "config": conf})
	})

	mux.HandleFunc("/api/peers/", func(w http.ResponseWriter, r *http.Request) {
		pub, action, err := peerRoute(r.URL.EscapedPath())
		if err != nil || pub == "" {
			http.Error(w, "no peer given", http.StatusBadRequest)
			return
		}
		if !requireAdmin(w, r, o) { // configs hold a private key, so reading is privileged too
			return
		}
		switch {
		case action == "" && r.Method == http.MethodDelete:
			if err := removePeer(o, pub); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case action == "config" && r.Method == http.MethodGet:
			servePeerFile(w, o, pub, false)
		case action == "qr" && r.Method == http.MethodGet:
			servePeerFile(w, o, pub, true)
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/admin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"admin": isAdmin(o, r)})
	})

	mux.HandleFunc("/api/update", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)

		if r.Method == http.MethodGet { // just report what is out there
			tag, err := latestTag()
			if err != nil {
				_ = enc.Encode(map[string]any{"version": Version, "error": err.Error()})
				return
			}
			_ = enc.Encode(map[string]any{"version": Version, "latest": tag, "available": newerThanRunning(tag)})
			return
		}
		// POST does the swap; a GET must never be able to trigger it
		if r.Method != http.MethodPost {
			http.Error(w, "use GET to check or POST to update", http.StatusMethodNotAllowed)
			return
		}
		if !requireAdmin(w, r, o) {
			return
		}
		// a slow download outlasts WriteTimeout, and a working update would look failed
		if rc := http.NewResponseController(w); rc != nil {
			_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))
		}
		tag, err := selfUpdate()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = enc.Encode(map[string]any{"error": err.Error()})
			return
		}
		_ = enc.Encode(map[string]any{"installed": tag})
		// the restart replaces this process, so let the response flush first
		go func() {
			time.Sleep(time.Second)
			if err := restartService(); err != nil {
				log.Printf("update installed but the restart failed: %v", err)
			}
		}()
	})

	if addrs := splitList(o.Admin); len(addrs) > 0 {
		log.Printf("admin actions allowed from %s and the loopback", strings.Join(addrs, ", "))
	} else {
		log.Print("admin actions allowed from the loopback only; add -admin <peer address> to manage from a peer")
	}
	log.Printf("tallyawg serving on http://%s (interface=%s, data=%s)", o.Listen, o.Interface, o.Data)
	srv := &http.Server{
		Addr:              o.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	log.Fatal(srv.ListenAndServe())
}

// peerRoute takes the escaped path: a base64 key holds "/", which decoding makes ambiguous.
func peerRoute(escaped string) (pub, action string, err error) {
	enc, action, _ := strings.Cut(strings.TrimPrefix(escaped, "/api/peers/"), "/")
	pub, err = url.PathUnescape(enc)
	return pub, action, err
}

func servePeerFile(w http.ResponseWriter, o *Options, pub string, qr bool) {
	confPath, qrPath, err := peerFiles(o, pub)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	path, ctype := confPath, "text/plain; charset=utf-8"
	if qr {
		path, ctype = qrPath, "image/png"
	}
	body, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "no stored file for this peer (it predates tally, or qrencode is missing)", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", ctype)
	if !qr {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	}
	_, _ = w.Write(body)
}

func maxZero(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

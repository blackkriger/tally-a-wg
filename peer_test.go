package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// the layout addPeer writes and a live server carries: the name comment sits inside the block
const threePeerConf = `[Interface]
Address = 10.9.0.1/24
ListenPort = 51820
PrivateKey = SRV

[Peer]
# alice
PublicKey = AAAAkey
AllowedIPs = 10.9.0.2/32

[Peer]
# bob
PublicKey = BBBBkey
AllowedIPs = 10.9.0.3/32

[Peer]
# carol
PublicKey = CCCCkey
AllowedIPs = 10.9.0.4/32
`

func TestRemovePeerFromConf(t *testing.T) {
	for _, tc := range []struct {
		drop  string
		keep  []string
		names map[string]string
	}{
		{"AAAAkey", []string{"BBBBkey", "CCCCkey"}, map[string]string{"BBBBkey": "bob", "CCCCkey": "carol"}},
		{"BBBBkey", []string{"AAAAkey", "CCCCkey"}, map[string]string{"AAAAkey": "alice", "CCCCkey": "carol"}},
		{"CCCCkey", []string{"AAAAkey", "BBBBkey"}, map[string]string{"AAAAkey": "alice", "BBBBkey": "bob"}},
	} {
		path := filepath.Join(t.TempDir(), "wg0.conf")
		if err := os.WriteFile(path, []byte(threePeerConf), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := removePeerFromConf(path, tc.drop); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got := string(body)
		if strings.Contains(got, tc.drop) {
			t.Errorf("dropping %s: still present\n%s", tc.drop, got)
		}
		for _, k := range tc.keep {
			if !strings.Contains(got, k) {
				t.Errorf("dropping %s: lost %s\n%s", tc.drop, k, got)
			}
		}
		if n := strings.Count(got, "[Peer]"); n != 2 {
			t.Errorf("dropping %s: %d [Peer] blocks left, want 2\n%s", tc.drop, n, got)
		}
		if !strings.Contains(got, "PrivateKey = SRV") {
			t.Errorf("dropping %s: lost the [Interface] section\n%s", tc.drop, got)
		}
		// a leftover "# name" would silently rename whichever peer follows it
		for pub, want := range tc.names {
			if n := namesFromConfig(path)[pub]; n != want {
				t.Errorf("dropping %s: %s is named %q, want %q\n%s", tc.drop, pub, n, want, got)
			}
		}
		if len(namesFromConfig(path)) != 2 {
			t.Errorf("dropping %s: names = %v, want 2\n%s", tc.drop, namesFromConfig(path), got)
		}
	}
}

func TestRemovePeerFromConfUnknownPeerKeepsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wg0.conf")
	if err := os.WriteFile(path, []byte(threePeerConf), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removePeerFromConf(path, "ZZZZkey"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if string(body) != threePeerConf {
		t.Errorf("unknown peer changed the file:\n%s", body)
	}
}

func TestParseAround(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		name, kind string
	}{
		{[]string{"alice"}, "alice", ""},
		{[]string{"-kind", "wg", "alice"}, "alice", "wg"},
		{[]string{"alice", "-kind", "wg"}, "alice", "wg"},
		{[]string{"alice", "-kind=wg"}, "alice", "wg"},
		{[]string{"-tz", "MSK", "alice", "-kind", "wg"}, "alice", "wg"},
		{[]string{"-kind", "wg"}, "", "wg"},
		{nil, "", ""},
	} {
		o := &Options{}
		fs := newFlags("peer add", o)
		fs.SetOutput(os.Stderr)
		kind := fs.String("kind", "", "")
		name := parseAround(fs, tc.args)
		if name != tc.name {
			t.Errorf("%v: name = %q, want %q", tc.args, name, tc.name)
		}
		if *kind != tc.kind {
			t.Errorf("%v: kind = %q, want %q", tc.args, *kind, tc.kind)
		}
	}
}

func TestPeerAddressIn(t *testing.T) {
	for _, tc := range []struct{ pub, want string }{
		{"AAAAkey", "10.9.0.2"},
		{"BBBBkey", "10.9.0.3"},
		{"CCCCkey", "10.9.0.4"},
		{"ZZZZkey", ""},
	} {
		if got := peerAddressIn(threePeerConf, tc.pub); got != tc.want {
			t.Errorf("%s: address = %q, want %q", tc.pub, got, tc.want)
		}
	}
}

// peers made before tally, or by another script, are not filed under their name
func TestClientConfWithAddress(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("wg0.conf", "[Interface]\nAddress = 10.9.0.1/24\nListenPort = 51820\nPrivateKey = SRV\n")
	write("client_bob.conf", "[Interface]\nPrivateKey = B\nAddress = 10.9.0.3/32\n")
	write("client_whatever.conf", "[Interface]\nPrivateKey = A\nAddress = 10.9.0.2/32, fd00::2/128\n")

	if got := clientConfWithAddress(dir, "10.9.0.2"); got != filepath.Join(dir, "client_whatever.conf") {
		t.Errorf("mismatched name: got %q", got)
	}
	if got := clientConfWithAddress(dir, "10.9.0.3"); got != filepath.Join(dir, "client_bob.conf") {
		t.Errorf("plain match: got %q", got)
	}
	if got := clientConfWithAddress(dir, "10.9.0.1"); got != "" {
		t.Errorf("the server config must never be served as a client config: got %q", got)
	}
	if got := clientConfWithAddress(dir, "10.9.0.9"); got != "" {
		t.Errorf("unknown address: got %q", got)
	}
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNamesFromConfigReadsCommentAbovePeer(t *testing.T) {
	path := write(t, "wg0.conf", `[Interface]
Address = 10.0.0.1/24

[Peer]
# laptop
PublicKey = AAAA1111
AllowedIPs = 10.0.0.2/32

[Peer]
# phone
PublicKey = BBBB2222
AllowedIPs = 10.0.0.3/32
`)
	got := namesFromConfig(path)
	if got["AAAA1111"] != "laptop" || got["BBBB2222"] != "phone" {
		t.Fatalf("got %v", got)
	}
}

// A [peer] header clears the pending comment, so a stray comment cannot leak onto the next peer.
func TestNamesFromConfigDropsCommentBeforePeerHeader(t *testing.T) {
	path := write(t, "wg0.conf", `# unrelated note
[Peer]
PublicKey = AAAA1111
`)
	if got := namesFromConfig(path); len(got) != 0 {
		t.Fatalf("want no names, got %v", got)
	}
}

func TestNamesFromConfigMissingFileIsEmpty(t *testing.T) {
	if got := namesFromConfig(filepath.Join(t.TempDir(), "nope.conf")); len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestNamesFromFileSplitsKeysByShape(t *testing.T) {
	path := write(t, "names", `
# comment
dGVzdHB1YmtleTEyMzQ1Njc4OTBhYmNkZWY= laptop
10.0.0.3 phone
fd00::1 v6 host
`)
	byPub, byIP := namesFromFile(path)
	if byPub["dGVzdHB1YmtleTEyMzQ1Njc4OTBhYmNkZWY="] != "laptop" {
		t.Fatalf("pubkey not parsed: %v", byPub)
	}
	if byIP["10.0.0.3"] != "phone" {
		t.Fatalf("address not parsed: %v", byIP)
	}
	if byIP["fd00::1"] != "v6 host" {
		t.Fatalf("IPv6 must land in the address map, got %v / %v", byPub, byIP)
	}
}

func TestNamesFromFileKeepsMultiWordNames(t *testing.T) {
	path := write(t, "names", "10.0.0.9 my home laptop\n")
	_, byIP := namesFromFile(path)
	if byIP["10.0.0.9"] != "my home laptop" {
		t.Fatalf("got %q", byIP["10.0.0.9"])
	}
}

func TestCleanIPStripsMaskAndExtras(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.2/32":             "10.0.0.2",
		"10.0.0.2/32,10.0.0.3/32": "10.0.0.2",
		" 10.0.0.4 ":              "10.0.0.4",
		"(none)":                  "", // wg prints this for an empty AllowedIPs
	} {
		if got := cleanIP(in); got != want {
			t.Errorf("cleanIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitListTrimsAndSkipsBlanks(t *testing.T) {
	got := splitList(" wg0 , awg0 ,, ")
	if len(got) != 2 || got[0] != "wg0" || got[1] != "awg0" {
		t.Fatalf("got %v", got)
	}
}

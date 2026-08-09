package main

import (
	"net/url"
	"testing"
)

func TestPeerRoute(t *testing.T) {
	// real keys: base64 puts "/", "+" and "=" into them, and decoding makes the first two split
	keys := []string{
		"rQYVllGhc1fz6jbAb3MA5IA9WhpWJ5PfT7ysOiOx/ic=",
		"J6VLAYU1OWKokNc0CYoRhSAN93Bbz+bVSwS2py2krxg=",
		"ydZCmGywGgsdyy35di1M8u6cv+eBv/REtpXVwuYYTg4=",
		"hGRKTZ7aDkjzBrqK5ygT0g0PEGGqRluV2DLaLPm0Jkc=",
	}
	for _, key := range keys {
		for _, action := range []string{"", "config", "qr"} {
			path := "/api/peers/" + url.PathEscape(key)
			if action != "" {
				path += "/" + action
			}
			u, err := url.Parse(path)
			if err != nil {
				t.Fatalf("parse %q: %v", path, err)
			}
			pub, got, err := peerRoute(u.EscapedPath())
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if pub != key {
				t.Errorf("%s: pub = %q, want %q", path, pub, key)
			}
			if got != action {
				t.Errorf("%s: action = %q, want %q", path, got, action)
			}
		}
	}
}

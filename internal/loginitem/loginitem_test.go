package loginitem

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestRenderPlistWellFormed(t *testing.T) {
	// Path with a space (e.g. "/Applications/K8s Port Forwards.app") and an
	// ampersand to exercise escaping.
	out := renderPlist([]string{"/usr/bin/open", "/Applications/A & B.app"})

	// Must be well-formed XML.
	dec := xml.NewDecoder(strings.NewReader(out))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("plist is not well-formed XML: %v\n%s", err, out)
		}
	}

	if !strings.Contains(out, "A &amp; B.app") {
		t.Fatalf("ampersand not escaped:\n%s", out)
	}
	if !strings.Contains(out, "<key>RunAtLoad</key>") || !strings.Contains(out, "<true/>") {
		t.Fatalf("missing RunAtLoad:\n%s", out)
	}
	if !strings.Contains(out, Label) {
		t.Fatalf("missing label:\n%s", out)
	}
}

func TestAppBundle(t *testing.T) {
	cases := []struct {
		exe        string
		wantBundle string
		wantOK     bool
	}{
		{"/Applications/K8s Port Forwards.app/Contents/MacOS/k8s-tray-forwarder",
			"/Applications/K8s Port Forwards.app", true},
		{"/Users/me/go/bin/k8s-tray-forwarder", "", false},
	}
	for _, c := range cases {
		got, ok := appBundle(c.exe)
		if ok != c.wantOK || got != c.wantBundle {
			t.Errorf("appBundle(%q) = (%q, %v), want (%q, %v)", c.exe, got, ok, c.wantBundle, c.wantOK)
		}
	}
}

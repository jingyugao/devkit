package service

import (
	"strings"
	"testing"
)

func TestRenderDarwinPlist(t *testing.T) {
	got := renderDarwinPlist("/usr/local/bin/keeprun")
	if !strings.Contains(got, "<string>/usr/local/bin/keeprun</string>") {
		t.Fatalf("plist missing executable: %s", got)
	}
	if !strings.Contains(got, "<string>daemon</string>") || !strings.Contains(got, "<string>serve</string>") {
		t.Fatalf("plist missing daemon args: %s", got)
	}
}

func TestRenderLinuxUnit(t *testing.T) {
	got := renderLinuxUnit("/usr/local/bin/keeprun")
	if !strings.Contains(got, "ExecStart=/usr/local/bin/keeprun daemon serve") {
		t.Fatalf("unit missing ExecStart: %s", got)
	}
	if !strings.Contains(got, "Restart=always") {
		t.Fatalf("unit missing restart policy: %s", got)
	}
}

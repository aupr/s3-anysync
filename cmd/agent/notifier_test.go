package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/aupr/s3-anysync/internal/notify"
)

func TestPickNotifier(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	n := pickNotifier(true, log)
	if _, ok := n.(logNotifier); !ok {
		t.Fatalf("noNotify=true: got %T, want logNotifier", n)
	}
	if err := n.Notify("Title", "Message"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"notification", "Title", "Message", `"level":"INFO"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q lacks %q", out, want)
		}
	}

	d := pickNotifier(false, log)
	if _, ok := d.(logNotifier); ok {
		t.Fatal("noNotify=false must not return logNotifier")
	}
	if _, ok := d.(notify.Notifier); !ok {
		t.Fatal("not a Notifier")
	}
}

func TestParseRunFlags(t *testing.T) {
	noTray, noNotify, verbose, err := parseRunFlags([]string{"--no-notify", "--no-tray"})
	if err != nil || !noTray || !noNotify || verbose {
		t.Fatalf("got %v %v %v %v", noTray, noNotify, verbose, err)
	}
	noTray, noNotify, verbose, err = parseRunFlags([]string{"--verbose"})
	if err != nil || noTray || noNotify || !verbose {
		t.Fatalf("got %v %v %v %v", noTray, noNotify, verbose, err)
	}
	if _, _, _, err := parseRunFlags([]string{"--bogus"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
	if _, _, _, err := parseRunFlags([]string{"extra"}); err == nil {
		t.Fatal("positional accepted")
	}
}

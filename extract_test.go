package main

import (
	"os"
	"testing"

	"howett.net/plist"
)

func TestParseFilesPlist(t *testing.T) {
	// files.plist: путь в ключе "file" (не "path"), нормализация "./x" -> "/x".
	input := map[string]interface{}{
		"files": []interface{}{
			map[string]interface{}{"file": "./usr/bin/foo", "sha256": "abc123"},
			map[string]interface{}{"file": "./etc/foo.conf", "sha256": "def456"},
		},
		"conf_files": []interface{}{
			map[string]interface{}{"file": "./etc/bar.conf", "sha256": "999"},
		},
	}
	data, err := plist.Marshal(input, plist.XMLFormat)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := parseFilesPlist(data)
	if err != nil {
		t.Fatalf("parseFilesPlist: %v", err)
	}
	if got["/usr/bin/foo"].SHA256 != "abc123" {
		t.Errorf("foo sha256=%q want abc123", got["/usr/bin/foo"].SHA256)
	}
	if got["/etc/foo.conf"].SHA256 != "def456" {
		t.Errorf("foo.conf sha256=%q want def456", got["/etc/foo.conf"].SHA256)
	}
	if got["/etc/bar.conf"].SHA256 != "999" {
		t.Errorf("bar.conf sha256=%q want 999", got["/etc/bar.conf"].SHA256)
	}
	if _, ok := got["usr/bin/foo"]; ok {
		t.Error("expected normalized key with leading slash, got bare key")
	}
}

func TestSha256File(t *testing.T) {
	f, err := os.CreateTemp("", "meow-sha-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Close()
	// sha256 пустого файла
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := sha256File(f.Name()); got != want {
		t.Errorf("sha256File=%q want %q", got, want)
	}
	if got := sha256File("/nonexistent/path"); got != "" {
		t.Errorf("expected empty for missing file, got %q", got)
	}
}

package main

import (
	"os"
	"path/filepath"
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

// setuid/setgid/sticky не должны теряться: Perm() режет эти биты, а без
// setuid-бита doas/passwd/su в установленной системе не работают.
func TestModeBitsPreserved(t *testing.T) {
	cases := []struct {
		mode os.FileMode
		want string
	}{
		{0755, "0755"},
		{0644, "0644"},
		{0755 | os.ModeSetuid, "4755"},
		{0755 | os.ModeSetgid, "2755"},
		{0777 | os.ModeSticky, "1777"},
		{0755 | os.ModeSetuid | os.ModeSetgid, "6755"},
		{os.ModeDir | 0755, "0755"},
	}
	for _, c := range cases {
		if got := modeString(c.mode); got != c.want {
			t.Errorf("modeString(%v) = %q, want %q", c.mode, got, c.want)
		}
		if got := permBits(c.mode); got != c.mode&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) {
			t.Errorf("permBits(%v) = %v", c.mode, got)
		}
	}
}

// place() должен выставить setuid на месте назначения (doas/passwd/su).
// Каталог создаём рядом с пакетом, а не в TMPDIR: /tmp часто смонтирован nosuid,
// и тогда setuid срезается ядром, а не нашим кодом.
func TestPlacePreservesSetuid(t *testing.T) {
	dir, err := os.MkdirTemp(".", "modetest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0755|os.ModeSetuid); err != nil {
		t.Skipf("chmod setuid недоступен: %v", err)
	}
	if st, err := os.Lstat(src); err != nil || st.Mode()&os.ModeSetuid == 0 {
		t.Skip("файловая система не сохраняет setuid (nosuid)")
	}
	dst := filepath.Join(dir, "out", "dst")
	if err := place(src, dst); err != nil {
		t.Fatalf("place: %v", err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSetuid == 0 {
		t.Errorf("setuid потерян: mode=%v", st.Mode())
	}
	if got := st.Mode().Perm(); got != 0755 {
		t.Errorf("perm=%o want 755", got)
	}
}

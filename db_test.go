package main

import (
	"testing"
)

func TestInstalledRoundtrip(t *testing.T) {
	old := dbDir
	dbDir = t.TempDir()
	defer func() { dbDir = old }()

	es := []Installed{
		{Name: "a", Version: "1.0", Date: "2024-01-01", Auto: true},
		{Name: "b", Version: "2.0", Date: "2024-01-02", Auto: false},
	}
	if err := writeInstalled(es); err != nil {
		t.Fatal(err)
	}
	got, err := readInstalled()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].Name != "a" || got[0].Version != "1.0" || !got[0].Auto {
		t.Errorf("got[0]=%+v", got[0])
	}
	if got[1].Name != "b" || got[1].Auto {
		t.Errorf("got[1]=%+v", got[1])
	}
}

func TestPkgRoundtrip(t *testing.T) {
	old := dbDir
	dbDir = t.TempDir()
	defer func() { dbDir = old }()

	p := &Pkg{
		Pkgname: "a",
		Version: "1.0",
		Files:   map[string]FileInfo{"/usr/bin/a": {Mode: "0755", SHA256: "x"}},
		Status:  "installed",
	}
	if err := writePkg(p); err != nil {
		t.Fatal(err)
	}
	got, err := readPkg("a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Pkgname != "a" || got.Version != "1.0" {
		t.Errorf("got=%+v", got)
	}
	if got.Files["/usr/bin/a"].SHA256 != "x" {
		t.Errorf("files=%+v", got.Files)
	}
}

package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type FileInfo struct {
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256,omitempty"`
}

type Pkg struct {
	Pkgname      string              `json:"pkgname"`
	Version      string              `json:"version"`
	Architecture string              `json:"architecture"`
	ShortDesc    string              `json:"short_desc"`
	RunDepends   []string            `json:"run_depends"`
	Provides     []string            `json:"provides"`
	Conflicts    []string            `json:"conflicts"`
	Files        map[string]FileInfo `json:"files"`
	EtcFiles     []string            `json:"etc_files"`
	InstallDate  string              `json:"install_date"`
	Auto         bool                `json:"auto"`
	Status       string              `json:"status"`
}

type Installed struct {
	Name    string
	Version string
	Date    string
	Auto    bool
}

func readInstalled() ([]Installed, error) {
	f, err := os.Open(filepath.Join(dbDir, "installed.list"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Installed
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), "|")
		if len(p) == 4 {
			out = append(out, Installed{p[0], p[1], p[2], p[3] == "1"})
		}
	}
	return out, sc.Err()
}

func writeInstalled(es []Installed) error {
	var b strings.Builder
	for _, e := range es {
		a := "0"
		if e.Auto {
			a = "1"
		}
		b.WriteString(e.Name)
		b.WriteByte('|')
		b.WriteString(e.Version)
		b.WriteByte('|')
		b.WriteString(e.Date)
		b.WriteByte('|')
		b.WriteString(a)
		b.WriteByte('\n')
	}
	return atomicWrite(filepath.Join(dbDir, "installed.list"), []byte(b.String()))
}

func readPkg(name string) (*Pkg, error) {
	data, err := os.ReadFile(filepath.Join(dbDir, name+".json"))
	if err != nil {
		return nil, err
	}
	var p Pkg
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func writePkg(p *Pkg) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dbDir, p.Pkgname+".json"), data)
}

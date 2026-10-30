package main

import (
	"bufio"
	"errors"
	"os"
	"regexp"
	"strings"
)

var escalatorRe = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

type Config struct {
	RepoMain    string
	RepoNonfree string
	Arch        string
	Escalator   string
	Ignore      map[string]bool
}

func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	c := &Config{Arch: "x86_64", Escalator: "doas", Ignore: map[string]bool{}}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "REPO_MAIN":
			c.RepoMain = v
		case "REPO_NONFREE":
			c.RepoNonfree = v
		case "ARCH":
			if v != "" {
				c.Arch = v
			}
		case "ESCALATOR":
			if escalatorRe.MatchString(v) {
				c.Escalator = v
			}
		case "IGNORE_PACKAGES":
			for _, n := range strings.Split(v, ",") {
				if n = strings.TrimSpace(n); n != "" {
					c.Ignore[n] = true
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if c.RepoMain == "" {
		return nil, errors.New("REPO_MAIN is required")
	}
	return c, nil
}

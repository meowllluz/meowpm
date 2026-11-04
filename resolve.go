package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type RepoCache struct {
	byName    map[string]*RepoPkg
	byProvide map[string][]*RepoPkg
}

func loadRepoCache() (*RepoCache, error) {
	files, _ := filepath.Glob(filepath.Join(cacheDir, "repo_*.json"))
	rc := &RepoCache{byName: map[string]*RepoPkg{}, byProvide: map[string][]*RepoPkg{}}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var pkgs []RepoPkg
		if err := json.Unmarshal(data, &pkgs); err != nil {
			return nil, err
		}
		for i := range pkgs {
			p := &pkgs[i]
			if old, ok := rc.byName[p.Pkgname]; !ok || CompareVersions(p.Version, old.Version) > 0 {
				rc.byName[p.Pkgname] = p
			}
		}
	}
	for _, p := range rc.byName {
		for _, pr := range p.Provides {
			// parseDep корректно разбирает "cmd:btop-1.4.7_1"
			// → name="cmd:btop", version="1.4.7_1".
			n, _ := parseDep(pr)
			rc.byProvide[n] = append(rc.byProvide[n], p)
		}
	}
	return rc, nil
}

func splitDepOp(s string) (string, string, string) {
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if i := strings.Index(s, op); i >= 0 {
			return s[:i], s[i+len(op):], op
		}
	}
	return s, "", ""
}

func parseDep(s string) (string, string) {
	if n, v, _ := splitDepOp(s); v != "" {
		return n, v
	}
	if i := strings.LastIndexByte(s, '-'); i > 0 {
		tail := s[i+1:]
		if tail != "" && tail[0] >= '0' && tail[0] <= '9' {
			return s[:i], tail
		}
	}
	return s, ""
}

func satisfiesDep(prov, req string) bool {
	pn, pv := parseDep(prov)
	rn, rv := parseDep(req)
	_, _, rop := splitDepOp(req)
	if pn != rn {
		return false
	}
	if rv == "" || pv == "" {
		return true
	}
	c := CompareVersions(pv, rv)
	switch rop {
	case ">=":
		return c >= 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case "<":
		return c < 0
	case "=":
		return c == 0
	}
	return c >= 0
}

func (rc *RepoCache) find(req string) (*RepoPkg, error) {
	n, _ := parseDep(req)
	if p, ok := rc.byName[n]; ok {
		return p, nil
	}
	var cands []*RepoPkg
	for _, p := range rc.byProvide[n] {
		for _, pr := range p.Provides {
			if satisfiesDep(pr, req) {
				cands = append(cands, p)
				break
			}
		}
	}
	switch len(cands) {
	case 0:
		return nil, fmt.Errorf("dependency '%s' not found", req)
	case 1:
		return cands[0], nil
	}
	names := make([]string, len(cands))
	for i, c := range cands {
		names[i] = c.Pkgname
	}
	sort.Strings(names)
	return nil, fmt.Errorf("multiple providers for '%s': %s", n, strings.Join(names, ", "))
}

const (
	white = 0
	gray  = 1
	black = 2
)

func (rc *RepoCache) resolveReal(req string) (string, error) {
	n, _ := parseDep(req)
	if _, ok := rc.byName[n]; ok {
		return n, nil
	}
	p, err := rc.find(req)
	if err != nil {
		return "", err
	}
	return p.Pkgname, nil
}

// resolveAll возвращает ВСЕ пакеты, удовлетворяющие зависимости (по имени или
// через provides). Для cascade: если провайдеров несколько — учитываем всех,
// чтобы не удалить ни одного из них как сироту.
func (rc *RepoCache) resolveAll(req string) []string {
	n, _ := parseDep(req)
	if p, ok := rc.byName[n]; ok {
		return []string{p.Pkgname}
	}
	var out []string
	for _, p := range rc.byProvide[n] {
		for _, pr := range p.Provides {
			if satisfiesDep(pr, req) {
				out = append(out, p.Pkgname)
				break
			}
		}
	}
	return out
}

// checkConflicts возвращает ошибку, если устанавливаемый пакет конфликтует
// с уже установленным (по имени пакета).
func checkConflicts(order []*RepoPkg, inst map[string]string) error {
	for _, p := range order {
		for _, c := range p.Conflicts {
			cn, _ := parseDep(c)
			if v, ok := inst[cn]; ok {
				return fmt.Errorf("package %s conflicts with installed %s-%s", p.Pkgname, cn, v)
			}
		}
	}
	return nil
}

// resolveOrder: трёхцветный итеративный DFS. Цикл детектится по реальному
// имени пакета (не по виртуальному provide-имени).
func resolveOrder(rc *RepoCache, cfg *Config, targets []string) ([]*RepoPkg, error) {
	color := map[string]int{}
	var order []*RepoPkg

	type frame struct{ name string }

	var visit func(root string) error
	visit = func(root string) error {
		stack := []frame{{root}}
		for len(stack) > 0 {
			f := stack[len(stack)-1]
			if color[f.name] == white {
				color[f.name] = gray
				var deps []string
				if p, ok := rc.byName[f.name]; ok {
					deps = p.RunDepends
				}
				for _, d := range deps {
					real, err := rc.resolveReal(d)
					if err != nil {
						return err
					}
					if cfg.Ignore[real] {
						return fmt.Errorf("dependency '%s' is in IGNORE_PACKAGES (needed by %s)", real, f.name)
					}
					switch color[real] {
					case gray:
						return fmt.Errorf("circular dependency: %s -> %s", f.name, real)
					case white:
						stack = append(stack, frame{real})
					}
				}
				continue
			}
			stack = stack[:len(stack)-1]
			if color[f.name] == gray {
				color[f.name] = black
				if p, ok := rc.byName[f.name]; ok {
					order = append(order, p)
				}
			}
		}
		return nil
	}

	for _, t := range targets {
		n, _ := parseDep(t)
		if cfg.Ignore[n] {
			return nil, fmt.Errorf("package %s is in IGNORE_PACKAGES", n)
		}
		real, err := rc.resolveReal(t)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve %q: %w (check the exact package name; if the index is empty or stale, run 'meow sync')", t, err)
		}
		if err := visit(real); err != nil {
			return nil, err
		}
	}
	return order, nil
}

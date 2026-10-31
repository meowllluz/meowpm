package main

import (
	"testing"
)

func TestSplitDepOp(t *testing.T) {
	cases := []struct {
		in            string
		name, ver, op string
	}{
		{"foo>=1.0", "foo", "1.0", ">="},
		{"foo<=1.0", "foo", "1.0", "<="},
		{"foo>1.0", "foo", "1.0", ">"},
		{"foo<1.0", "foo", "1.0", "<"},
		{"foo=1.0", "foo", "1.0", "="},
		{"foo", "foo", "", ""},
		{"foo-bar", "foo-bar", "", ""},
	}
	for _, c := range cases {
		n, v, op := splitDepOp(c.in)
		if n != c.name || v != c.ver || op != c.op {
			t.Errorf("splitDepOp(%q)=%q,%q,%q want %q,%q,%q", c.in, n, v, op, c.name, c.ver, c.op)
		}
	}
}

func TestParseDep(t *testing.T) {
	cases := []struct{ in, name, ver string }{
		{"cmd:btop-1.4.7_1", "cmd:btop", "1.4.7_1"},
		{"foo>=1.0", "foo", "1.0"},
		{"bar", "bar", ""},
		{"libfoo-2", "libfoo", "2"},
		{"python3", "python3", ""},
		{"foo-1.0", "foo", "1.0"},
	}
	for _, c := range cases {
		n, v := parseDep(c.in)
		if n != c.name || v != c.ver {
			t.Errorf("parseDep(%q)=%q,%q want %q,%q", c.in, n, v, c.name, c.ver)
		}
	}
}

func TestSatisfiesDep(t *testing.T) {
	cases := []struct {
		prov, req string
		want      bool
	}{
		{"foo-2.0", "foo>=1.0", true},
		{"foo-1.0", "foo>=2.0", false},
		{"bar-1.0", "foo>=1.0", false},
		{"foo-1.0", "foo", true}, // req без версии — любая
		{"foo-2.0", "foo=2.0", true},
		{"foo-1.9", "foo=2.0", false},
	}
	for _, c := range cases {
		if got := satisfiesDep(c.prov, c.req); got != c.want {
			t.Errorf("satisfiesDep(%q,%q)=%v want %v", c.prov, c.req, got, c.want)
		}
	}
}

func TestResolveOrder(t *testing.T) {
	rc := &RepoCache{
		byName: map[string]*RepoPkg{
			"a": {Pkgname: "a", Version: "1.0", RunDepends: []string{"b", "c"}},
			"b": {Pkgname: "b", Version: "1.0", RunDepends: []string{"c"}},
			"c": {Pkgname: "c", Version: "1.0"},
		},
		byProvide: map[string][]*RepoPkg{},
	}
	cfg := &Config{Ignore: map[string]bool{}}
	order, err := resolveOrder(rc, cfg, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range order {
		names = append(names, p.Pkgname)
	}
	// зависимости раньше зависимых: c -> b -> a
	want := []string{"c", "b", "a"}
	if len(names) != len(want) {
		t.Fatalf("len=%d want %d (%v)", len(names), len(want), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order=%v want %v", names, want)
		}
	}
}

func TestResolveOrderCircular(t *testing.T) {
	rc := &RepoCache{
		byName: map[string]*RepoPkg{
			"a": {Pkgname: "a", Version: "1.0", RunDepends: []string{"b"}},
			"b": {Pkgname: "b", Version: "1.0", RunDepends: []string{"a"}},
		},
		byProvide: map[string][]*RepoPkg{},
	}
	cfg := &Config{Ignore: map[string]bool{}}
	if _, err := resolveOrder(rc, cfg, []string{"a"}); err == nil {
		t.Fatal("expected circular dependency error")
	}
}

func TestCheckConflicts(t *testing.T) {
	order := []*RepoPkg{{Pkgname: "a", Version: "1.0", Conflicts: []string{"x"}}}
	if err := checkConflicts(order, map[string]string{"x": "1.0"}); err == nil {
		t.Error("expected conflict error for installed x")
	}
	if err := checkConflicts(order, map[string]string{"y": "1.0"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveAll(t *testing.T) {
	rc := &RepoCache{
		byName: map[string]*RepoPkg{
			"foo": {Pkgname: "foo", Version: "1.0"},
		},
		byProvide: map[string][]*RepoPkg{
			"awk": {
				{Pkgname: "gawk", Version: "1.0", Provides: []string{"awk-0"}},
				{Pkgname: "mawk", Version: "1.0", Provides: []string{"awk-0"}},
			},
		},
	}
	if got := rc.resolveAll("foo"); len(got) != 1 || got[0] != "foo" {
		t.Errorf("resolveAll(foo)=%v want [foo]", got)
	}
	got := rc.resolveAll("awk>=0")
	if len(got) != 2 {
		t.Fatalf("resolveAll(awk>=0)=%v want 2 providers", got)
	}
}

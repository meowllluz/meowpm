package main

import (
	"strconv"
	"strings"
)

// CompareVersions returns -1, 0, 1. Port of xbps_cmpver.
// Revision after '_' is compared as a separate integer.
// Base versions use dewey_cmp rules.
func CompareVersions(a, b string) int {
	av, ar := splitRevision(a)
	bv, br := splitRevision(b)
	if c := deweyCmp(av, bv); c != 0 {
		return c
	}
	return cmpInt(ar, br)
}

func splitRevision(s string) (string, int) {
	i := strings.LastIndexByte(s, '_')
	if i < 0 {
		return s, 0
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return s, 0
	}
	return s[:i], n
}

type part struct {
	num   int
	str   string
	isNum bool
}

func (p part) absent() bool { return !p.isNum && p.str == "" }

func deweyCmp(a, b string) int {
	ap, bp := splitParts(a), splitParts(b)
	n := len(ap)
	if len(bp) > n {
		n = len(bp)
	}
	for i := 0; i < n; i++ {
		var x, y part
		if i < len(ap) {
			x = ap[i]
		}
		if i < len(bp) {
			y = bp[i]
		}
		if c := cmpPart(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func splitParts(s string) []part {
	var out []part
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			n, _ := strconv.Atoi(s[i:j])
			out = append(out, part{isNum: true, num: n})
			i = j
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			j := i
			for j < len(s) {
				ch := s[j]
				if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')) {
					break
				}
				j++
			}
			out = append(out, part{str: s[i:j]})
			i = j
		default:
			i++
		}
	}
	return out
}

func cmpPart(a, b part) int {
	switch {
	case a.absent() && b.absent():
		return 0
	case a.absent():
		if b.isNum {
			return cmpInt(0, b.num)
		}
		if sp := specialValue(b.str); sp != 0 {
			return cmpInt(0, sp)
		}
		return -1
	case b.absent():
		if a.isNum {
			return cmpInt(a.num, 0)
		}
		if sp := specialValue(a.str); sp != 0 {
			return cmpInt(sp, 0)
		}
		return 1
	case a.isNum && b.isNum:
		return cmpInt(a.num, b.num)
	case a.isNum:
		return 1
	case b.isNum:
		return -1
	}
	as, bs := specialValue(a.str), specialValue(b.str)
	if as != bs {
		return cmpInt(as, bs)
	}
	return strings.Compare(a.str, b.str)
}

// specialValue: alpha<beta<pre<rc<""<pl. Anything else = 0.
func specialValue(s string) int {
	switch strings.ToLower(s) {
	case "alpha":
		return -4
	case "beta":
		return -3
	case "pre":
		return -2
	case "rc":
		return -1
	case "pl":
		return 1
	}
	return 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

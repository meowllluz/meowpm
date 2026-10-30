package main

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		{"1.0", "1.0.1", -1},
		{"1.0.1", "1.0", 1},
		{"1.0.1", "1.0.2", -1},
		{"2.0", "10.0", -1},
		{"10.0", "2.0", 1},
		{"1.0.0", "1.0", 0},
		{"1.0", "1.0.0", 0},

		{"1.0_1", "1.0_2", -1},
		{"1.0_2", "1.0_1", 1},
		{"1.0_2", "1.0.1", -1},
		{"1.0", "1.0_0", 0},
		{"1.2.3_10", "1.2.3_1", 1},
		{"1.2.3_1", "1.2.3_10", -1},

		{"1.0alpha", "1.0", -1},
		{"1.0", "1.0alpha", 1},
		{"1.0alpha1", "1.0alpha2", -1},
		{"1.0alpha", "1.0alpha1", -1},
		{"1.0beta", "1.0", -1},
		{"1.0rc1", "1.0", -1},
		{"1.0pre", "1.0", -1},
		{"1.0pre1", "1.0", -1},
		{"1.0alpha", "1.0beta", -1},
		{"1.0beta", "1.0pre", -1},
		{"1.0pre", "1.0rc", -1},
		{"1.0rc", "1.0", -1},

		{"1.0pl1", "1.0", 1},
		{"1.0", "1.0pl1", -1},
		{"1.0pl", "1.0", 1},
		{"1.0rc1", "1.0pl1", -1},

		{"1.0a", "1.0", 1},
		{"1.0", "1.0a", -1},
		{"1.0a", "1.0a1", -1},

		{"1.0a", "1.0.1", -1},
		{"1.0", "1.a", 1},

		{"0.1", "0.10", -1},
		{"0.10", "0.1", 1},
		{"1.1.1", "1.1", 1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
		if got := CompareVersions(c.b, c.a); got != -c.want {
			t.Errorf("antisymmetry CompareVersions(%q,%q)=%d want %d", c.b, c.a, got, -c.want)
		}
	}
}

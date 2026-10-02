package glob

import "testing"

func TestMatches(t *testing.T) {
	cases := []struct {
		glob, file string
		want       bool
	}{
		{"src/billing/**", "src/billing/a/b.ts", true},
		{"src/*.ts", "src/a.ts", true},
		{"src/*.ts", "src/a/b.ts", false},
		{"src/api", "src/api/x.ts", true},
		{"src/api", "src/apix/x.ts", false},
		{"**/*.md", "docs/a/b.md", true},
	}
	for _, c := range cases {
		if got := Matches(c.glob, c.file); got != c.want {
			t.Errorf("Matches(%q,%q)=%v want %v", c.glob, c.file, got, c.want)
		}
	}
}

func TestOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"src/billing/**", "src/billing/*.ts", true},
		{"src/api/billing.ts", "src/api/*.ts", true},
		{"src/api/billing.ts", "src/api/*.md", false},
		{"src/billing/**", "src/auth/**", false},
		{"src/billing", "src/billing/**", true},
		{"src/**", "src/web/x.ts", true},
	}
	for _, c := range cases {
		if got := Overlap(c.a, c.b); got != c.want {
			t.Errorf("Overlap(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

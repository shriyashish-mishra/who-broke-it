package rules

import (
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
)

func TestParseAndEnforce(t *testing.T) {
	rs := Parse(DefaultConstitution + "- **C9** No eval. `paths: src/**` `except: src/legacy/**` `forbid: /\\beval\\(/`\n")
	if len(rs) < 6 {
		t.Fatalf("want >=6 rules, got %d", len(rs))
	}
	hits := CheckLines(rs, []gitx.FileLines{
		{File: "src/a.ts", Lines: []string{"const x = eval(y)"}},
		{File: "src/legacy/b.ts", Lines: []string{"eval(z)"}},
		{File: "lib/c.ts", Lines: []string{"eval(q)"}},
	})
	if len(hits) != 1 || hits[0].File != "src/a.ts" {
		t.Fatalf("expected exactly src/a.ts, got %+v", hits)
	}
	secret := CheckLines(rs, []gitx.FileLines{{File: "x.ts", Lines: []string{`const apiKey = "abcdefghijklmnop1234"`}}})
	if len(secret) != 1 || secret[0].Rule.ID != "C4" {
		t.Fatalf("expected C4 secret violation, got %+v", secret)
	}
}

// Package glob implements minimal glob matching (`**`, `*`, `?`) and conservative glob-vs-glob overlap detection.
package glob

import (
	"regexp"
	"strings"
	"sync"
)

func hasWild(s string) bool { return strings.ContainsAny(s, "*?") }

// Norm normalizes separators, strips a leading "./" and expands a trailing "/" to "/**".
func Norm(p string) string {
	s := strings.ReplaceAll(p, "\\", "/")
	s = strings.TrimPrefix(s, "./")
	if strings.HasSuffix(s, "/") {
		s += "**"
	}
	return s
}

var cache sync.Map

// ToRegexp compiles a glob to an anchored regexp.
func ToRegexp(glob string) *regexp.Regexp {
	g := Norm(glob)
	if v, ok := cache.Load(g); ok {
		return v.(*regexp.Regexp)
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch {
		case c == '*':
			if i+1 < len(g) && g[i+1] == '*' {
				if i+2 < len(g) && g[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re := regexp.MustCompile(b.String())
	cache.Store(g, re)
	return re
}

// Matches reports whether a glob (or literal file/dir) covers a file path.
func Matches(glob, file string) bool {
	f := strings.TrimPrefix(strings.ReplaceAll(file, "\\", "/"), "./")
	g := Norm(glob)
	if !hasWild(g) {
		return f == g || strings.HasPrefix(f, g+"/")
	}
	return ToRegexp(g).MatchString(f)
}

func MatchesAny(globs []string, file string) bool {
	for _, g := range globs {
		if Matches(g, file) {
			return true
		}
	}
	return false
}

func prefixSegs(g string) []string {
	var out []string
	for _, s := range strings.Split(Norm(g), "/") {
		if hasWild(s) {
			break
		}
		out = append(out, s)
	}
	return out
}

func isPrefix(a, b []string) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Overlap is a conservative test: true if some file could be matched by both globs.
// It may report overlap that cannot occur; it never misses one.
func Overlap(a, b string) bool {
	na, nb := Norm(a), Norm(b)
	wa, wb := hasWild(na), hasWild(nb)
	switch {
	case !wa && !wb:
		return na == nb || strings.HasPrefix(na, nb+"/") || strings.HasPrefix(nb, na+"/")
	case !wa:
		return Matches(nb, na) || isPrefix(strings.Split(na, "/"), prefixSegs(nb))
	case !wb:
		return Matches(na, nb) || isPrefix(strings.Split(nb, "/"), prefixSegs(na))
	}
	pa, pb := prefixSegs(na), prefixSegs(nb)
	return isPrefix(pa, pb) || isPrefix(pb, pa)
}

// AnyOverlap reports whether any glob in as overlaps any glob in bs.
func AnyOverlap(as, bs []string) bool {
	for _, a := range as {
		for _, b := range bs {
			if Overlap(a, b) {
				return true
			}
		}
	}
	return false
}

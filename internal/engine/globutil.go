package engine

import "github.com/shriyashish-mishra/who-broke-it/internal/glob"

func globOverlap(a, b string) bool { return glob.Overlap(a, b) }

package engine

import "wbi/internal/glob"

func globOverlap(a, b string) bool { return glob.Overlap(a, b) }

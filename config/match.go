// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

package config

import (
	"path/filepath"
	"regexp"
	"strings"
)

// compile converts a glob pattern into a regexp.  Ordinarily we could just use
// filepath.Match, but tarsnap allows "**" and {a,b} notation, which filepath
// does not support. This translation is imperfect.
func compile(pat string) string {
	var cmp strings.Builder
	cmp.WriteRune('^')
	var brack int
	var star, class bool
	for _, ch := range pat {
		// Handle "*" and "**" patterns. When we see a star, check whether the
		// previous character was a star too.
		if ch == '*' && star {
			star = false
			cmp.WriteString(`.*?`) // anything including separators
			continue
		} else if ch == '*' {
			star = true // not sure if we have * or **
			continue
		}

		// Now we know ch != '*', so write out a buffered one if there is.
		if star {
			star = false
			cmp.WriteString(`[^/]*`) // anything except separators
		}

		if ch == '?' {
			cmp.WriteString(`[^/]`) // any non-separator
		} else if ch == '{' {
			brack++
			cmp.WriteString("(?:") // {a,b,c} → (?:a|b|c)
		} else if ch == ',' && brack > 0 {
			cmp.WriteRune('|')
		} else if ch == '}' && brack > 0 {
			brack--
			cmp.WriteRune(')')
		} else if (ch == '[' && !class) || (ch == ']' && class) {
			class = !class
			cmp.WriteRune(ch)
		} else {
			cmp.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	if star {
		cmp.WriteString(`[^/]*`)
	}

	// The pattern must match a complete directory name, but need not consume
	// the rest of the path.
	cmp.WriteString(`(?:/|$)`)
	return cmp.String()
}

func pathMatchesPattern(path, pat string) bool {
	cmp := compile(pat)
	ok, err := regexp.MatchString(cmp, path)
	return err == nil && ok
}

func containsPath(b *Backup, wd, path string) (string, bool) {
	// Normalize the path to be relative to where this backup was created.
	base := b.WorkDir
	if base == "" {
		base = wd
	}
	needle := path
	if !filepath.IsAbs(path) {
		rel, err := filepath.Rel(base, filepath.Join(base, path))
		if err != nil {
			return path, false
		}
		needle = rel
	} else if t := strings.TrimPrefix(path, base+"/"); t != path {
		needle = t
	}

	// The resulting path is captured if it matches at least one inclusion and
	// does not match any exclusions. Check the exclusions first so that we can
	// short circuit out of the inclusion check.
	for _, ex := range b.Exclude {
		if pathMatchesPattern(needle, ex) {
			return needle, false
		}
	}
	for _, in := range b.Include {
		if needle == in || strings.HasPrefix(needle, in+"/") {
			return needle, true
		}
	}
	return needle, false
}

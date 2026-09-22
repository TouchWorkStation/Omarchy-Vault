package shortcuts

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxSourceDepth = 8
	maxConfigFiles = 200
)

var (
	varDef   = regexp.MustCompile(`^\$([A-Za-z0-9_]+)\s*=\s*(.*)$`)
	bindLine = regexp.MustCompile(`^bind([a-z]*)\s*=\s*(.*)$`)
	varRef   = regexp.MustCompile(`\$([A-Za-z0-9_]+)`)
)

type confParser struct {
	home     string
	vars     map[string]string
	visited  map[string]bool
	files    []string
	binds    []Binding
	warnings []string
	submap   string
}

// parseConfigTree reads a Hyprland config and every file it sources, and
// returns the effective keybindings in the default submap. It only reads.
func parseConfigTree(root, home string) ([]Binding, []string, []string) {
	p := &confParser{home: home, vars: map[string]string{}, visited: map[string]bool{}}
	if _, err := os.Stat(root); err != nil {
		return nil, nil, nil
	}
	p.parseFile(root, 0)
	return p.binds, p.files, p.warnings
}

func (p *confParser) expand(path, relTo string) string {
	path = p.subst(strings.TrimSpace(path))
	if strings.HasPrefix(path, "~/") && p.home != "" {
		path = filepath.Join(p.home, path[2:])
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(relTo), path)
	}
	return filepath.Clean(path)
}

func (p *confParser) subst(s string) string {
	// Replace longer names first is unnecessary with a regex callback.
	return varRef.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := p.vars[m[1:]]; ok {
			return v
		}
		return m
	})
}

func (p *confParser) parseFile(path string, depth int) {
	if depth > maxSourceDepth {
		p.warnings = append(p.warnings, "Hyprland config sources are nested too deeply; stopped at "+path)
		return
	}
	if p.visited[path] || len(p.visited) >= maxConfigFiles {
		return
	}
	p.visited[path] = true

	f, err := os.Open(path)
	if err != nil {
		p.warnings = append(p.warnings, fmt.Sprintf("Could not read %s", path))
		return
	}
	defer f.Close()
	p.files = append(p.files, path)

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := stripComment(sc.Text())
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := varDef.FindStringSubmatch(line); m != nil {
			p.vars[m[1]] = p.subst(strings.TrimSpace(m[2]))
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch {
		case key == "source":
			target := p.expand(val, path)
			matches, err := filepath.Glob(target)
			if err != nil || len(matches) == 0 {
				continue
			}
			for _, m := range matches {
				p.parseFile(m, depth+1)
			}
		case key == "submap":
			if val == "reset" {
				p.submap = ""
			} else {
				p.submap = val
			}
		case key == "unbind":
			if p.submap != "" {
				continue
			}
			parts := splitFields(p.subst(val), 2)
			if len(parts) < 2 {
				continue
			}
			mods, k := parseMods(parts[0]), parts[1]
			kept := p.binds[:0]
			for _, b := range p.binds {
				if !sameCombo(b.Mods, b.Key, mods, k) {
					kept = append(kept, b)
				}
			}
			p.binds = kept
		default:
			m := bindLine.FindStringSubmatch(line)
			if m == nil || p.submap != "" {
				continue
			}
			if b, ok := parseBind(m[1], p.subst(m[2]), path); ok {
				p.binds = append(p.binds, b)
			}
		}
	}
}

// parseBind parses the value of a bind line. With the "d" flag the third
// field is a description.
func parseBind(flags, val, source string) (Binding, bool) {
	hasDesc := strings.Contains(flags, "d")
	if strings.Contains(flags, "m") {
		return Binding{}, false // mouse binds
	}
	n := 4
	if hasDesc {
		n = 5
	}
	parts := splitFields(val, n)
	if len(parts) < 3 {
		return Binding{}, false
	}
	b := Binding{Mods: parseMods(parts[0]), Key: strings.TrimSpace(parts[1]), Source: source}
	rest := parts[2:]
	if hasDesc && len(rest) > 0 {
		b.Description = rest[0]
		rest = rest[1:]
	}
	if len(rest) > 0 {
		b.Dispatcher = rest[0]
	}
	if len(rest) > 1 {
		b.Arg = rest[1]
	}
	if b.Key == "" {
		return Binding{}, false
	}
	return b, true
}

// splitFields splits on commas into at most n fields, trimming each; the
// last field keeps any remaining commas (exec arguments often contain them).
func splitFields(s string, n int) []string {
	parts := strings.SplitN(s, ",", n)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// stripComment removes a trailing # comment; "##" is a literal '#'.
func stripComment(s string) string {
	const placeholder = "\x00HASH\x00"
	s = strings.ReplaceAll(s, "##", placeholder)
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	return strings.ReplaceAll(s, placeholder, "#")
}

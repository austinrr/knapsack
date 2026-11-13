package config

import (
	"bytes"
	"fmt"
	"strings"
)

// Marshal returns c as TOML, in the same layout Save writes. It doesn't
// validate.
func Marshal(c *Config) []byte {
	var buf bytes.Buffer
	encode(&buf, c)
	return buf.Bytes()
}

// encode writes c as TOML in a fixed layout: top-level keys, then one table
// per profile in name order, then source tables that have a key. Each addon
// is on its own line, so adding or removing one is a one-line diff.
//
// It's hand-written because the TOML library indents nested tables, adds an
// empty [profiles] header, and puts each array on a single line. Profile
// names are validated to be bare keys, so only string values need escaping.
func encode(buf *bytes.Buffer, c *Config) {
	section := func() {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
	}

	if c.CurrentProfile != "" {
		fmt.Fprintf(buf, "current_profile = %s\n", quote(c.CurrentProfile))
	}

	for _, name := range c.ProfileNames() {
		p := c.Profiles[name]
		section()
		fmt.Fprintf(buf, "[profiles.%s]\n", name)
		fmt.Fprintf(buf, "path = %s\n", quote(p.Path))
		fmt.Fprintf(buf, "flavor = %s\n", quote(string(p.Flavor)))
		if len(p.Addons) == 0 {
			buf.WriteString("addons = []\n")
			continue
		}
		buf.WriteString("addons = [\n")
		for _, r := range p.Addons {
			fmt.Fprintf(buf, "  %s,\n", quote(r.String()))
		}
		buf.WriteString("]\n")
	}

	for _, ks := range keyedSources {
		key := ks.field(&c.Sources).APIKey
		if key == "" {
			continue
		}
		section()
		fmt.Fprintf(buf, "[sources.%s]\n", ks.name)
		fmt.Fprintf(buf, "api_key = %s\n", quote(key))
	}
}

// quote returns s as a TOML basic string. Per the TOML spec, '"', '\' and
// control characters must be escaped; everything else, including non-ASCII
// UTF-8, is written as is. (strconv.Quote isn't used because its \x escapes
// aren't valid TOML.)
func quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

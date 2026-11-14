package tomlenc

import (
	"fmt"
	"strings"
	"time"
)

// Quote returns s as a TOML basic string, for a value or a quoted key. Per the TOML spec, '"', '\' and
// control characters must be escaped; everything else, including non-ASCII
// UTF-8, is written as is. (strconv.Quote isn't used because its \x escapes
// aren't valid TOML.)
func Quote(s string) string {
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

// Time formats t as a TOML offset date-time in UTC, to the second:
// 2026-11-13T18:04:05Z.
func Time(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// Strings formats ss as a TOML array of strings on one line:
// ["Details", "Details_DataStorage"].
func Strings(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = Quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

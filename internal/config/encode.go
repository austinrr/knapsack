package config

import (
	"bytes"
	"fmt"

	"github.com/austinrr/knapsack/internal/tomlenc"
)

// MarshalProfile returns one profile's settings as TOML, without the
// [profiles.<name>] header, as ParseProfile reads them.
func MarshalProfile(p Profile) []byte {
	var buf bytes.Buffer
	encodeProfile(&buf, p)
	return buf.Bytes()
}

// encodeProfile writes a profile's keys, one addon per line.
func encodeProfile(buf *bytes.Buffer, p Profile) {
	fmt.Fprintf(buf, "path = %s\n", tomlenc.Quote(p.Path))
	fmt.Fprintf(buf, "flavor = %s\n", tomlenc.Quote(string(p.Flavor)))
	if len(p.Addons) == 0 {
		buf.WriteString("addons = []\n")
		return
	}
	buf.WriteString("addons = [\n")
	for _, r := range p.Addons {
		fmt.Fprintf(buf, "  %s,\n", tomlenc.Quote(r.String()))
	}
	buf.WriteString("]\n")
}

// Marshal returns c as TOML, in the same layout Save writes. It doesn't
// validate.
func Marshal(c *Config) []byte {
	var buf bytes.Buffer
	encode(&buf, c)
	return buf.Bytes()
}

// encode writes c as TOML in a fixed layout: top-level keys, then one table
// per profile in name order, then source tables that have settings. Each addon
// is on its own line, so adding or removing one is a one-line diff.
//
// It's hand-written because the TOML library indents nested tables, adds an
// empty [profiles] header, and puts each array on a single line. Profile
// names are validated to be bare keys, so only string values need escaping,
// which tomlenc.Quote does.
func encode(buf *bytes.Buffer, c *Config) {
	section := func() {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
	}

	if c.CurrentProfile != "" {
		fmt.Fprintf(buf, "current_profile = %s\n", tomlenc.Quote(c.CurrentProfile))
	}

	for _, name := range c.ProfileNames() {
		section()
		fmt.Fprintf(buf, "[profiles.%s]\n", name)
		encodeProfile(buf, c.Profiles[name])
	}

	for _, ks := range keyedSources {
		f := ks.field(&c.Sources)
		if f.APIKey == "" && f.API == "" {
			continue
		}
		section()
		fmt.Fprintf(buf, "[sources.%s]\n", ks.name)
		if f.API != "" {
			fmt.Fprintf(buf, "api = %s\n", tomlenc.Quote(string(f.API)))
		}
		if f.APIKey != "" {
			fmt.Fprintf(buf, "api_key = %s\n", tomlenc.Quote(f.APIKey))
		}
	}
}

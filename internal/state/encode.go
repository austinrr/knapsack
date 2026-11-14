package state

import (
	"bytes"
	"fmt"

	"github.com/austinrr/knapsack/internal/tomlenc"
)

// Marshal returns st as TOML, in the layout Save writes: the schema version,
// then one table per addon in ref order, separated by blank lines. It
// doesn't validate.
//
// It's hand-written, like config's encoder, because the TOML library adds an
// empty [addons] header and runs the tables together, which makes the file
// harder to read when debugging.
func Marshal(st *State) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "version = %d\n", SchemaVersion)
	for _, r := range st.Refs() {
		a := st.Addons[r]
		fmt.Fprintf(&buf, "\n[addons.%s]\n", tomlenc.Quote(r.String()))
		fmt.Fprintf(&buf, "version = %s\n", tomlenc.Quote(a.Version))
		fmt.Fprintf(&buf, "sha256 = %s\n", tomlenc.Quote(a.SHA256))
		fmt.Fprintf(&buf, "installed_at = %s\n", tomlenc.Time(a.InstalledAt))
		fmt.Fprintf(&buf, "folders = %s\n", tomlenc.Strings(a.Folders))
	}
	return buf.Bytes()
}

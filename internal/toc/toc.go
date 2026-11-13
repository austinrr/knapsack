package toc

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// File is a parsed TOC file's metadata. File lists aren't kept: Knapsack
// never needs them.
type File struct {
	// Interfaces are the ## Interface values, in file order. Entries that
	// aren't numbers are skipped, as the client effectively does.
	Interfaces []int
	// AllowLoadGameTypes and ExcludeLoadGameTypes are lower-cased game
	// types. Repeated lines add to the list rather than replacing it.
	AllowLoadGameTypes   []string
	ExcludeLoadGameTypes []string

	// headers maps lower-cased keys to values. A later line with the same
	// key replaces an earlier one.
	headers map[string]string
}

// Get returns the value of a header such as "Title" or "Version", or "" if
// it's absent. Keys are matched without regard to case.
func (f File) Get(key string) string {
	return f.headers[strings.ToLower(key)]
}

// bom is the UTF-8 byte order mark some editors put at the start of a file.
const bom = "\xef\xbb\xbf"

// maxLine bounds a single line. Real TOC lines are well under 1 KiB; the
// limit just keeps a corrupt file from using unbounded memory.
const maxLine = 1 << 20

// Parse reads a TOC file. It accepts a UTF-8 byte order mark and CRLF line
// endings. Lines that aren't "## Key: Value" headers (comments, file names,
// blank lines) are skipped. Only a read error or an overlong line fails.
func Parse(r io.Reader) (File, error) {
	f := File{headers: map[string]string{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			line = strings.TrimPrefix(line, bom)
			first = false
		}
		f.addLine(strings.TrimSuffix(line, "\r"))
	}
	if err := sc.Err(); err != nil {
		return File{}, fmt.Errorf("read toc: %w", err)
	}
	return f, nil
}

// addLine records one line if it's a header.
func (f *File) addLine(line string) {
	rest, ok := strings.CutPrefix(line, "##")
	if !ok {
		return // a comment ("# ..."), a file name, or blank
	}
	key, value, ok := strings.Cut(rest, ":")
	if !ok {
		return
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if key == "" {
		return
	}

	switch key {
	case "interface":
		f.Interfaces = parseInterfaces(value)
	case "allowloadgametype":
		f.AllowLoadGameTypes = append(f.AllowLoadGameTypes, parseList(value)...)
	case "excludeloadgametype":
		f.ExcludeLoadGameTypes = append(f.ExcludeLoadGameTypes, parseList(value)...)
	}
	f.headers[key] = value
}

// parseInterfaces reads "120105, 16001, 50504". A later ## Interface line
// replaces an earlier one, as with any other header.
func parseInterfaces(value string) []int {
	var out []int
	for _, field := range parseList(value) {
		if n, err := strconv.Atoi(field); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// parseList splits a comma- or space-separated list, lower-casing each
// entry: "mainline, Classic" becomes ["mainline", "classic"].
func parseList(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
}

package flavor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// buildInfoFile is the file in the WoW folder where the launcher records
// each installed client's version.
const buildInfoFile = ".build.info"

// ClientInterface reads the installed client's version from .build.info in
// the WoW folder and returns its Interface number (1.60.1 → 16001).
//
// The WoW folder is the parent of the client folder in addonsDir:
// …/World of Warcraft/_forever_/Interface/AddOns. ClientInterface reports
// false when there is no client folder in the path, the file is missing or
// unreadable, it has no row for the flavor's product, or the product code
// isn't known yet, or the version isn't in the flavor's Interface range,
// which means the table or the product code is wrong. Callers then skip the out-of-date check, which is the
// only thing the exact number is for.
func ClientInterface(addonsDir string, f Flavor) (int, bool) {
	c, ok := f.Client()
	if !ok || c.Product == "" {
		return 0, false
	}
	root, ok := wowFolder(addonsDir, c.Folder)
	if !ok {
		return 0, false
	}
	file, err := os.Open(filepath.Join(root, buildInfoFile))
	if err != nil {
		return 0, false
	}
	defer func() { _ = file.Close() }() // read-only; nothing to flush

	version, err := productVersion(file, c.Product)
	if err != nil {
		return 0, false
	}
	n, err := interfaceNumber(version)
	if err != nil || !c.Interface.Contains(n) {
		return 0, false
	}
	return n, true
}

// wowFolder returns the parent of the path element named clientFolder,
// matched without regard to case as in FromPath, searching from the end.
func wowFolder(addonsDir, clientFolder string) (string, bool) {
	dir := filepath.Clean(addonsDir)
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false // reached the root
		}
		if strings.EqualFold(filepath.Base(dir), clientFolder) {
			return parent, true
		}
		dir = parent
	}
}

// productVersion returns the Version of product's row in a .build.info
// file. The file is pipe-separated with a header row whose cells look like
// "Version!STRING:0": a column name, then its type, which isn't needed.
// When the file has an Active column, only a row marked active counts: the
// launcher keeps inactive rows for clients that are no longer installed.
func productVersion(r io.Reader, product string) (string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 1<<20) // rows list CDN hosts and tags, well over 1 KiB
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", errors.New("empty file")
	}
	cols := map[string]int{}
	for i, cell := range strings.Split(strings.TrimSuffix(sc.Text(), "\r"), "|") {
		name, _, _ := strings.Cut(cell, "!")
		cols[strings.ToLower(name)] = i
	}
	productCol, ok1 := cols["product"]
	versionCol, ok2 := cols["version"]
	if !ok1 || !ok2 {
		return "", errors.New("no Product or Version column")
	}
	activeCol, hasActive := cols["active"]

	for sc.Scan() {
		row := strings.Split(strings.TrimSuffix(sc.Text(), "\r"), "|")
		if productCol >= len(row) || versionCol >= len(row) || row[productCol] != product {
			continue
		}
		if hasActive && (activeCol >= len(row) || row[activeCol] != "1") {
			continue
		}
		return row[versionCol], nil
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no active row for product %q", product)
}

// interfaceNumber turns a client version like "1.60.1.70938" into its
// Interface number: major·10000 + minor·100 + patch, so 16001. The build
// number after the patch is ignored.
func interfaceNumber(version string) (int, error) {
	parts := strings.Split(version, ".")
	if len(parts) < 3 {
		return 0, fmt.Errorf("version %q: want major.minor.patch", version)
	}
	var n [3]int
	for i := range n {
		v, err := strconv.Atoi(parts[i])
		if err != nil || v < 0 || (i > 0 && v > 99) {
			return 0, fmt.Errorf("version %q: bad number %q", version, parts[i])
		}
		n[i] = v
	}
	if n[0] == 0 {
		return 0, fmt.Errorf("version %q: major version is 0", version)
	}
	return n[0]*10000 + n[1]*100 + n[2], nil
}

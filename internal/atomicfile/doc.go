// Package atomicfile replaces files so readers see either the old contents or
// the new, never a partial write: write a temp file in the same directory,
// fsync it, then rename it over the target.
package atomicfile

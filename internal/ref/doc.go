// Package ref parses and normalizes addon refs: the `source:id` names that
// identify an addon in config, such as github:austinrr/baganator-mirror.
//
// Parse also accepts each source's web URLs and turns them into a ref, so an
// addon has exactly one spelling no matter how the user typed it.
package ref

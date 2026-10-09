// Package build tests sbx's build command and the fixture-based build and
// run translations, by driving the CLI in-process against the fake
// backends. It lives beside the command layer it exercises; the tests use
// only the exported Run entry point.
package build

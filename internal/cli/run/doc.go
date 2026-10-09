// Package run tests sbx's disposable-run command and the project-volume
// wiring, by driving the CLI in-process against the fake backends. It
// lives beside the command layer it exercises; the tests use only the
// exported Run entry point.
package run

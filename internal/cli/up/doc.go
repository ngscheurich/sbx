// Package up tests sbx's persistent-sandbox commands — up, exec, stop,
// logs, status, rm — by driving the CLI in-process against the fake
// backends. It lives beside the command layer it exercises; the tests use
// only the exported Run entry point.
package up

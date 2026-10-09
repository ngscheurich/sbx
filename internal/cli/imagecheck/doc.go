// Package imagecheck tests the image_check gate and the plan's reporting
// of it, by driving the CLI in-process against the fake backends. It lives
// beside the command layer it exercises; the tests use only the exported
// Run entry point.
package imagecheck

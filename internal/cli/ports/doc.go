// Package ports tests how the commands reserve and publish stable loopback
// ports, by driving the CLI in-process against the fake backends. It is a
// separate test package because its tests bind fixed loopback ports and
// therefore cannot share a test process with the others. The tests use
// only the exported Run entry point of the command layer.
package ports

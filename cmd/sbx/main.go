// Command sbx is the entry point of the sbx CLI.
package main

import (
	"os"

	"github.com/ngscheurich/sbx/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
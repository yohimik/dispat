// Command dispat builds and publishes the changed packages of a monorepo.
//
// It reads a root configuration file, discovers packages inside configured
// spaces, decides new semantic versions from conventional commits found in git
// history, and then builds and publishes changed packages in parallel while
// respecting the dependency graph.
package main

import (
	"os"

	"github.com/yohimik/dispat/services/dispat/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}

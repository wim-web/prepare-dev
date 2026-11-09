// Command prepare-dev connects vague intent to requirements, decisions, design,
// checkable specs and verification evidence. See "prepare-dev help".
package main

import (
	"os"

	"github.com/wim-web/prepare-dev/internal/cli"
	"github.com/wim-web/prepare-dev/internal/daemon"
)

// version is set by release builds: -ldflags "-X main.version=0.2.0".
var version string

func main() {
	if version != "" {
		daemon.Version = version
	}
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}

package main

import (
	"fmt"
	"os"

	"github.com/tf-triage/tf-triage/cmd"
)

// Set via ldflags at build time by GoReleaser.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	cmd.SetVersion(version, commit)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

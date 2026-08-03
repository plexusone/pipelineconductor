// Package main provides the CLI entry point for PipelineConductor.
package main

import (
	"fmt"
	"os"

	"github.com/plexusone/pipelineconductor/cmd/pipelineconductor/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

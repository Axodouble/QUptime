// Package main is the entry point for the qu binary.
//
// qu is a quorum-based uptime monitor. Multiple cooperating nodes
// run identical copies of this binary; they elect a master that
// owns alert dispatch and check aggregation while every node
// independently probes the configured targets.
package main

import (
	"fmt"
	"os"

	"github.com/jasper/quptime/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// Package cli wires every user-facing command on the qu binary.
//
// The root command is built lazily via NewRootCommand so test code
// can construct a fresh tree per invocation. Each subcommand lives
// in its own file (init.go, serve.go, node.go, …) and is attached
// from NewRootCommand below.
package cli

import "github.com/spf13/cobra"

// NewRootCommand returns the full cobra tree.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "qu",
		Short:         "Quorum-based uptime monitor",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addInitCmd(root)
	addServeCmd(root)
	addNodeCmd(root)
	addCheckCmd(root)
	addAlertCmd(root)
	addTrustCmd(root)
	addStatusCmd(root)
	return root
}

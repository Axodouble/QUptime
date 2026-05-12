package cli

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"git.cer.sh/axodouble/quptime/internal/daemon"
)

func addServeCmd(root *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the qu daemon in the foreground",
		Long: `Run the qu daemon: starts the inter-node listener, the local
control socket for the CLI, the heartbeat loop and the check
scheduler. Stops cleanly on SIGINT or SIGTERM.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.New(os.Stderr, "quptime: ", log.LstdFlags|log.Lmsgprefix)
			d, err := daemon.New(logger)
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return d.Run(ctx)
		},
	}
	root.AddCommand(cmd)
}

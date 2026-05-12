package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"git.cer.sh/axodouble/quptime/internal/daemon"
	"git.cer.sh/axodouble/quptime/internal/trust"
)

func addTrustCmd(root *cobra.Command) {
	t := &cobra.Command{
		Use:   "trust",
		Short: "Inspect and edit the local trust store",
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "Print all trusted peer fingerprints",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			raw, err := callDaemon(ctx, daemon.CtrlTrustList, nil)
			if err != nil {
				return err
			}
			var entries []trust.Entry
			if err := json.Unmarshal(raw, &entries); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NODE_ID\tADDRESS\tFINGERPRINT\tADDED")
			for _, e := range entries {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
					e.NodeID, e.Address, e.Fingerprint, e.AddedAt.Format(time.RFC3339))
			}
			return tw.Flush()
		},
	}
	t.AddCommand(list)

	remove := &cobra.Command{
		Use:   "remove <node-id>",
		Short: "Drop a peer from the local trust store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			body := daemon.NodeRemoveBody{NodeID: args[0]}
			raw, err := callDaemon(ctx, daemon.CtrlTrustRemove, body)
			if err != nil {
				return err
			}
			var res struct {
				Removed bool `json:"removed"`
			}
			_ = json.Unmarshal(raw, &res)
			if res.Removed {
				fmt.Fprintf(cmd.OutOrStdout(), "trust entry %s removed\n", args[0])
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "no trust entry for %s\n", args[0])
			}
			return nil
		},
	}
	t.AddCommand(remove)

	root.AddCommand(t)
}

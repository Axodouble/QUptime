package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jasper/quptime/internal/daemon"
)

func addNodeCmd(root *cobra.Command) {
	node := &cobra.Command{
		Use:   "node",
		Short: "Manage cluster membership",
	}

	add := &cobra.Command{
		Use:   "add <host:port>",
		Short: "Trust-on-first-use add a peer to this cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			return runNodeAdd(ctx, cmd, args[0])
		},
	}
	add.Flags().BoolP("yes", "y", false, "skip interactive confirmation")
	node.AddCommand(add)

	list := &cobra.Command{
		Use:   "list",
		Short: "List configured peers and their last-seen status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			return runStatusPrint(ctx, cmd, true)
		},
	}
	node.AddCommand(list)

	remove := &cobra.Command{
		Use:   "remove <node-id>",
		Short: "Remove a peer from the cluster and trust store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			body := daemon.NodeRemoveBody{NodeID: args[0]}
			raw, err := callDaemon(ctx, daemon.CtrlNodeRemove, body)
			if err != nil {
				return err
			}
			var res daemon.MutateResult
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s (cluster version now %d)\n", args[0], res.Version)
			return nil
		},
	}
	node.AddCommand(remove)

	root.AddCommand(node)
}

// runNodeAdd does a two-step TOFU: probe peer, confirm fingerprint
// interactively, then issue the actual add.
func runNodeAdd(ctx context.Context, cmd *cobra.Command, addr string) error {
	probeBody := daemon.NodeProbeBody{Address: addr}
	raw, err := callDaemon(ctx, daemon.CtrlNodeProbe, probeBody)
	if err != nil {
		return fmt.Errorf("probe %s: %w", addr, err)
	}
	var probe daemon.NodeProbeResult
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "remote node id : %s\n", probe.NodeID)
	fmt.Fprintf(cmd.OutOrStdout(), "fingerprint    : %s\n", probe.Fingerprint)

	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		fmt.Fprint(cmd.OutOrStdout(), "trust this peer? [y/N] ")
		ans, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans != "y" && ans != "yes" {
			return fmt.Errorf("aborted")
		}
	}

	addBody := daemon.NodeAddBody{Address: addr, Fingerprint: probe.Fingerprint}
	raw, err = callDaemon(ctx, daemon.CtrlNodeAdd, addBody)
	if err != nil {
		return err
	}
	var res daemon.NodeAddResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "added node %s (cluster version now %d)\n", res.NodeID, res.Version)
	return nil
}

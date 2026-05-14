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

	"git.cer.sh/axodouble/quptime/internal/config"
	"git.cer.sh/axodouble/quptime/internal/daemon"
	"git.cer.sh/axodouble/quptime/internal/transport"
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

	node.AddCommand(buildNodeEditCmd())

	root.AddCommand(node)
}

// buildNodeEditCmd returns `qu node edit`, which currently only updates
// the peer's advertise address. The NodeID, fingerprint, and certificate
// are part of the cluster's trust relationship and cannot be edited —
// remove and re-add the node (with the new cert) if those need to change.
func buildNodeEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit <node-id>",
		Short: "Update the advertise address (host:port) of an existing peer",
		Long: `Update fields of an existing peer.

Only the advertise address is editable — the NodeID, fingerprint, and
certificate are bound by trust and cannot be changed in place. To change
those, remove the node and add it again (which re-performs TOFU).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			if !cmd.Flags().Changed("address") {
				return fmt.Errorf("--address is required")
			}
			newAddr, _ := cmd.Flags().GetString("address")
			newAddr = strings.TrimSpace(newAddr)
			if newAddr == "" {
				return fmt.Errorf("--address cannot be empty")
			}

			cluster, err := config.LoadClusterConfig()
			if err != nil {
				return err
			}
			snap := cluster.Snapshot()
			var existing *config.PeerInfo
			for i := range snap.Peers {
				if snap.Peers[i].NodeID == args[0] {
					cp := snap.Peers[i]
					existing = &cp
					break
				}
			}
			if existing == nil {
				return fmt.Errorf("no peer with node id %q", args[0])
			}
			existing.Advertise = newAddr

			payload, err := json.Marshal(existing)
			if err != nil {
				return err
			}
			body := daemon.MutateBody{Kind: transport.MutationAddPeer, Payload: payload}
			raw, err := callDaemon(ctx, daemon.CtrlMutate, body)
			if err != nil {
				return err
			}
			var res daemon.MutateResult
			_ = json.Unmarshal(raw, &res)
			fmt.Fprintf(cmd.OutOrStdout(), "updated peer %s -> %s (cluster version now %d)\n",
				existing.NodeID, existing.Advertise, res.Version)
			return nil
		},
	}
	cmd.Flags().String("address", "", "new host:port advertise address")
	return cmd
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

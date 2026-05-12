package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"git.cer.sh/axodouble/quptime/internal/config"
	"git.cer.sh/axodouble/quptime/internal/crypto"
)

func addInitCmd(root *cobra.Command) {
	var advertise string
	var bindAddr string
	var bindPort int

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate node identity, keys, and config",
		Long: `Initialise a new qu node on this host: pick a UUID, generate an
RSA keypair, write a default node.yaml, and prepare the trust store.

Idempotent in one direction only: existing key material is never
overwritten. Re-run only after wiping the data directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.EnsureDataDir(); err != nil {
				return err
			}
			if _, err := os.Stat(config.NodeFilePath()); err == nil {
				return errors.New("node.yaml already exists in data dir — refusing to overwrite")
			}
			nodeID := uuid.NewString()
			n := &config.NodeConfig{
				NodeID:    nodeID,
				BindAddr:  bindAddr,
				BindPort:  bindPort,
				Advertise: advertise,
			}
			if err := n.Save(); err != nil {
				return fmt.Errorf("save node.yaml: %w", err)
			}
			if _, err := crypto.GenerateKeyPair(nodeID); err != nil {
				return fmt.Errorf("generate keys: %w", err)
			}

			// Seed cluster.yaml with this node as its own first peer.
			// Without this the math in `quorum` would treat a one-node
			// cluster as "0 peers, fallback quorum=1, master=self" —
			// which works in isolation but breaks the moment another
			// node joins, because the replicated peers list would lack
			// the inviter, leading to split-brain elections.
			certPEM, err := crypto.LoadCertPEM()
			if err != nil {
				return fmt.Errorf("load cert: %w", err)
			}
			fp, err := crypto.FingerprintFromCertPEM(certPEM)
			if err != nil {
				return fmt.Errorf("fingerprint own cert: %w", err)
			}
			cluster := &config.ClusterConfig{}
			if err := cluster.Mutate(nodeID, func(c *config.ClusterConfig) error {
				c.Peers = []config.PeerInfo{{
					NodeID:      nodeID,
					Advertise:   n.AdvertiseAddr(),
					Fingerprint: fp,
				}}
				return nil
			}); err != nil {
				return fmt.Errorf("seed cluster.yaml: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "initialised node %s\n", nodeID)
			fmt.Fprintf(cmd.OutOrStdout(), "data dir: %s\n", config.DataDir())
			fmt.Fprintf(cmd.OutOrStdout(), "advertise: %s\n", n.AdvertiseAddr())
			return nil
		},
	}
	cmd.Flags().StringVar(&advertise, "advertise", "", "address peers should use to reach this node (host:port)")
	cmd.Flags().StringVar(&bindAddr, "bind", "0.0.0.0", "listen address for inter-node traffic")
	cmd.Flags().IntVar(&bindPort, "port", 9001, "listen port for inter-node traffic")
	root.AddCommand(cmd)
}

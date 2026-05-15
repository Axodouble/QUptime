package cli

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
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
	var clusterSecret string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate node identity, keys, and config",
		Long: `Initialise a new qu node on this host: pick a UUID, generate an
RSA keypair, write a default node.yaml, and prepare the trust store.

Pass --secret on every subsequent node so they share the same
cluster join secret. If --secret is omitted on the very first node, a
random secret is generated and printed for the operator to copy.

Every flag may also be supplied via its QUPTIME_* environment variable
(see docs/configuration.md). Explicit flags win over env values, which
in turn win over the compiled defaults.

Idempotent in one direction only: existing key material is never
overwritten. Re-run only after wiping the data directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(config.NodeFilePath()); err == nil {
				return errors.New("node.yaml already exists in data dir — refusing to overwrite")
			}

			// Only let env fill fields the operator did NOT pass on the
			// command line; explicit flags must win over env.
			n := &config.NodeConfig{}
			if cmd.Flags().Changed("bind") {
				n.BindAddr = bindAddr
			}
			if cmd.Flags().Changed("port") {
				n.BindPort = bindPort
			}
			if cmd.Flags().Changed("advertise") {
				n.Advertise = advertise
			}
			if cmd.Flags().Changed("secret") {
				n.ClusterSecret = clusterSecret
			}
			if err := n.ApplyEnvOverrides(); err != nil {
				return err
			}
			// Cobra defaults (bind=0.0.0.0, port=9901) are still
			// available as fallbacks for fields neither flag nor env
			// touched.
			if n.BindAddr == "" {
				n.BindAddr = bindAddr
			}
			if n.BindPort == 0 {
				n.BindPort = bindPort
			}

			_, generated, err := bootstrapNode(n)
			if err != nil {
				return err
			}
			printBootstrapResult(cmd.OutOrStdout(), n, generated)
			return nil
		},
	}
	cmd.Flags().StringVar(&advertise, "advertise", "", "address peers should use to reach this node (host:port)")
	cmd.Flags().StringVar(&bindAddr, "bind", "0.0.0.0", "listen address for inter-node traffic")
	cmd.Flags().IntVar(&bindPort, "port", 9901, "listen port for inter-node traffic")
	cmd.Flags().StringVar(&clusterSecret, "secret", "", "shared cluster join secret (omit on the first node to auto-generate)")
	root.AddCommand(cmd)
}

// bootstrapNode creates the data dir, writes node.yaml, generates the
// keypair, and seeds cluster.yaml with this node as its own first
// peer. cfg may arrive with any subset of fields populated; missing
// NodeID and ClusterSecret are auto-generated, missing BindAddr /
// BindPort get the compiled defaults.
//
// Returns the populated config (the same pointer that was passed in)
// and a flag indicating whether ClusterSecret was generated here. The
// flag exists so the caller can print the secret for the operator —
// it must be copied to every follower node out-of-band.
//
// Caller is responsible for checking that node.yaml does not yet
// exist; bootstrapNode itself will refuse to overwrite an existing
// keypair (crypto.GenerateKeyPair errors out) but does not guard
// against clobbering node.yaml.
func bootstrapNode(cfg *config.NodeConfig) (*config.NodeConfig, bool, error) {
	if err := config.EnsureDataDir(); err != nil {
		return nil, false, err
	}
	if cfg.NodeID == "" {
		cfg.NodeID = uuid.NewString()
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = "0.0.0.0"
	}
	if cfg.BindPort == 0 {
		cfg.BindPort = 9901
	}
	generated := false
	if cfg.ClusterSecret == "" {
		s, err := generateSecret()
		if err != nil {
			return nil, false, fmt.Errorf("generate cluster secret: %w", err)
		}
		cfg.ClusterSecret = s
		generated = true
	}
	if err := cfg.Save(); err != nil {
		return nil, false, fmt.Errorf("save node.yaml: %w", err)
	}
	if _, err := crypto.GenerateKeyPair(cfg.NodeID); err != nil {
		return nil, false, fmt.Errorf("generate keys: %w", err)
	}

	// Seed cluster.yaml with this node as its own first peer.
	// Without this the math in `quorum` would treat a one-node
	// cluster as "0 peers, fallback quorum=1, master=self" — which
	// works in isolation but breaks the moment another node joins,
	// because the replicated peers list would lack the inviter,
	// leading to split-brain elections.
	certPEM, err := crypto.LoadCertPEM()
	if err != nil {
		return nil, false, fmt.Errorf("load cert: %w", err)
	}
	fp, err := crypto.FingerprintFromCertPEM(certPEM)
	if err != nil {
		return nil, false, fmt.Errorf("fingerprint own cert: %w", err)
	}
	cluster := &config.ClusterConfig{}
	if err := cluster.Mutate(cfg.NodeID, func(c *config.ClusterConfig) error {
		c.Peers = []config.PeerInfo{{
			NodeID:      cfg.NodeID,
			Advertise:   cfg.AdvertiseAddr(),
			Fingerprint: fp,
			CertPEM:     string(certPEM),
		}}
		return nil
	}); err != nil {
		return nil, false, fmt.Errorf("seed cluster.yaml: %w", err)
	}
	return cfg, generated, nil
}

// printBootstrapResult emits the human-readable summary both `qu init`
// and the serve auto-init path print after bootstrapping. Kept in one
// place so the secret-disclosure format stays identical across the two
// entry points.
func printBootstrapResult(out io.Writer, n *config.NodeConfig, secretGenerated bool) {
	fmt.Fprintf(out, "initialised node %s\n", n.NodeID)
	fmt.Fprintf(out, "data dir: %s\n", config.DataDir())
	fmt.Fprintf(out, "advertise: %s\n", n.AdvertiseAddr())
	if secretGenerated {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "cluster secret (copy to every other node via --secret or QUPTIME_CLUSTER_SECRET):")
		fmt.Fprintln(out, "  "+n.ClusterSecret)
	}
}

// generateSecret produces 32 bytes of crypto-random data and returns
// it base64-encoded. Long enough that brute force isn't a concern;
// short enough that operators can copy-paste it without pagination.
func generateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

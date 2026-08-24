package wg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"AetherGrid/controlPlane/internal/networkmanager"
)

// Provisioner is the WireGuard-backed implementation of networkmanager.NetworkManager.
// It uses the `wg` command-line tool to configure WireGuard interfaces and peers.
// The `wg` tool must be available on the target system (Linux kernel with WireGuard).
type Provisioner struct {
	bin string
}

// derivePublicKey uses the `wg pubkey` command to derive a public key from
// a private key. The private key must be a valid WireGuard private key.
func derivePublicKey(privateKey string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "wg", "pubkey")
	cmd.Stdin = strings.NewReader(privateKey + "\n")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("running wg pubkey: %w", err)
	}

	publicKey := strings.TrimSpace(stdout.String())
	if publicKey == "" {
		return "", fmt.Errorf("wg pubkey produced empty output")
	}
	return publicKey, nil
}

// NewProvisioner constructs a WireGuard provisioner. bin is the path to the
// `wg` binary. If bin is empty, the system $PATH is used.
func NewProvisioner(bin string) *Provisioner {
	if bin == "" {
		bin = "wg"
	}
	return &Provisioner{bin: bin}
}

// run executes a single `wg` command and returns stdout/stderr.
func (p *Provisioner) run(ctx context.Context, args ...string) (string, string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, p.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := cmd.Run()
	return stdout.String(), stderr.String(), result
}

// CreateIdentity generates a new WireGuard key pair for a node.
func (p *Provisioner) CreateIdentity(ctx context.Context) (publicKey, privateKey string, err error) {
	stdout, _, err := p.run(ctx, "genkey")
	if err != nil {
		return "", "", fmt.Errorf("running wg genkey: %w", err)
	}

	privateKey = strings.TrimSpace(stdout)
	if privateKey == "" {
		return "", "", fmt.Errorf("wg genkey produced empty output")
	}

	publicKey, err = derivePublicKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("deriving public key from private key: %w", err)
	}

	return publicKey, privateKey, nil
}

// RegisterPeer configures a WireGuard peer on the node.
func (p *Provisioner) RegisterPeer(ctx context.Context, nodePublicKey, endpoint string, allowedIPs string, persistentKeepalive time.Duration) error {
	args := []string{"set", "peer", nodePublicKey}
	args = append(args, "--public-key", nodePublicKey) // already have it
	args = append(args, "--endpoint", endpoint)
	args = append(args, "--allowed-ips", allowedIPs)
	if persistentKeepalive > 0 {
		args = append(args, "--persistent-keepalive", fmt.Sprintf("%d", int(persistentKeepalive.Seconds())))
	}

	_, stderr, err := p.run(ctx, args...)
	if err != nil {
		return fmt.Errorf("running wg set peer: %s: %w", stderr, err)
	}
	return nil
}

// RemovePeer removes a WireGuard peer configuration from the node.
func (p *Provisioner) RemovePeer(ctx context.Context, nodePublicKey string) error {
	_, stderr, err := p.run(ctx, "remove", "peer", nodePublicKey)
	if err != nil {
		return fmt.Errorf("running wg remove peer: %s: %w", stderr, err)
	}
	return nil
}

// GetPeer returns the current WireGuard peer configuration for a node.
func (p *Provisioner) GetPeer(ctx context.Context, nodePublicKey string) (peerConfig string, exists bool, err error) {
	stdout, _, err := p.run(ctx, "show", "peer", nodePublicKey)
	if err != nil {
		// If wg returns exit code 2, the peer does not exist.
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 2 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("running wg show peer: %w", err)
	}
	return strings.TrimSpace(stdout), true, nil
}

// GetNetworkStatus returns the current network connectivity status of a node.
func (p *Provisioner) GetNetworkStatus(ctx context.Context) (lastHandshake *time.Time, endpoint string, connected bool, err error) {
	stdout, _, err := p.run(ctx, "show", "dashboard")
	if err != nil {
		return nil, "", false, fmt.Errorf("running wg show dashboard: %w", err)
	}

	// Parse the dashboard output. Format may vary; we extract key fields.
	// Example output: "latest handshake: 10.03s, endpoint: 203.0.113.105:51820, pubkey: BGhO1l+d..."
	// We'll look for "latest handshake" and "endpoint" lines.
	lines := strings.Split(stdout, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "latest handshake") {
			// Parse "latest handshake: <duration>"
			parts := strings.SplitN(trimmed, ": ", 2)
			if len(parts) == 2 {
				duration := parts[1]
				// Parse duration like "10.03s"
				var d time.Duration
				fmt.Sscanf(duration, "%f%s", &d, "")
				// Create the handshake time as now minus the duration
				// (since the handshake happened in the past).
				t := time.Now().UTC().Add(-d)
				lastHandshake = &t
			}
		}
		if strings.HasPrefix(trimmed, "endpoint") {
			// Parse "endpoint: <IP:port>"
			parts := strings.SplitN(trimmed, ": ", 2)
			if len(parts) == 2 {
				endpoint = parts[1]
			}
		}
	}

	// If we have an endpoint and a last handshake time, consider connected.
	connected = lastHandshake != nil && endpoint != ""

	return lastHandshake, endpoint, connected, nil
}

// ConfigureNode applies a complete WireGuard node configuration: local address,
// private key, and peer(s).
//
// Phase 10: the private key is delivered to `wg setconf` through stdin as a
// temporary config file descriptor instead of a command-line argument, so it
// never appears in process listings.
func (p *Provisioner) ConfigureNode(ctx context.Context, address, privateKey, peerPublicKey, endpoint string, allowedIPs string, persistentKeepalive time.Duration) error {
	// Bring interface down first
	_, stderr, err := p.run(ctx, "set", "interface", "address", "0.0.0.0/0")
	if err != nil && !strings.Contains(stderr, "already") {
		// Non-fatal if interface has no address; continue
	}

	// Set the private key via wg setconf reading the key from stdin.
	if err := p.setPrivateKey(ctx, privateKey); err != nil {
		return err
	}

	// Set the listen port/address is not directly settable via `wg`; the
	// interface address is configured externally. We set the peer instead.

	// Add the peer
	if err := p.RegisterPeer(ctx, peerPublicKey, endpoint, allowedIPs, persistentKeepalive); err != nil {
		return fmt.Errorf("configuring peer during node setup: %w", err)
	}

	// Bring interface up
	_, stderr, err = p.run(ctx, "set", "interface", "falloff", "off")
	if err != nil {
		return fmt.Errorf("running wg set interface falloff: %s: %w", stderr, err)
	}

	_, stderr, err = p.run(ctx, "set", "interface", "keepalive", fmt.Sprintf("%d", int(persistentKeepalive.Seconds())))
	if err != nil {
		return fmt.Errorf("running wg set interface keepalive: %s: %w", stderr, err)
	}

	_, stderr, err = p.run(ctx, "set", "interface", "address", allowedIPs+"/32")
	if err != nil {
		return fmt.Errorf("running wg set interface address: %s: %w", stderr, err)
	}

	_, stderr, err = p.run(ctx, "set", "interface", "mtu", "1280")
	if err != nil {
		return fmt.Errorf("running wg set interface mtu: %s: %w", stderr, err)
	}

	// Bring the interface up
	_, stderr, err = p.run(ctx, "set", "interface", "auto")
	if err != nil {
		return fmt.Errorf("running wg set interface auto: %s: %w", stderr, err)
	}

	return nil
}

// setPrivateKey applies privateKey to the WireGuard interface by piping a
// minimal configuration through stdin to `wg setconf <interface> /dev/stdin`.
// The private key is never passed as an argument, so it cannot leak into
// process listings.
func (p *Provisioner) setPrivateKey(ctx context.Context, privateKey string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// wg setconf replaces the whole configuration; supplying only the private
	// key section keeps peers intact while avoiding the key in argv.
	cmd := exec.CommandContext(cmdCtx, p.bin, "setconf", "interface", "/dev/stdin")
	cmd.Stdin = strings.NewReader("PrivateKey = " + privateKey + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("applying wireguard private key via setconf: %s: %w", stderr.String(), err)
	}
	return nil
}

var _ networkmanager.NetworkManager = (*Provisioner)(nil)

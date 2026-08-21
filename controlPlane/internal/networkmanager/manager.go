package networkmanager

import (
	"context"
	"time"
)

// NetworkManager is the abstraction responsible for WireGuard network
// operations: key generation, peer management, and network configuration.
// The rest of AETHER-GRID communicates with NetworkManager rather than with
// wg, wg-quick, or Linux networking commands directly, allowing the
// implementation to be swapped or tested without system dependencies.
type NetworkManager interface {
	// CreateIdentity generates a new WireGuard key pair for a node and
	// persists it. The private key must remain on the node; the control
	// plane only ever sees the public key.
	CreateIdentity(ctx context.Context) (publicKey, privateKey string, err error)

	// RegisterPeer configures a WireGuard peer on the node. This typically
	// means writing the peer's public key, endpoint, and AllowedIPs into the
	// node's WireGuard interface configuration.
	RegisterPeer(ctx context.Context, nodePublicKey, endpoint string, allowedIPs string, persistentKeepalive time.Duration) error

	// RemovePeer removes a WireGuard peer configuration from the node.
	RemovePeer(ctx context.Context, nodePublicKey string) error

	// GetPeer returns the current WireGuard peer configuration for a node,
	// or indicates that no peer is configured.
	GetPeer(ctx context.Context, nodePublicKey string) (peerConfig string, exists bool, err error)

	// GetNetworkStatus returns the current network connectivity status of a
	// node, including the latest handshake time, endpoint, and connection state.
	GetNetworkStatus(ctx context.Context) (lastHandshake *time.Time, endpoint string, connected bool, err error)

	// ConfigureNode applies a complete WireGuard node configuration: local
	// address, private key, and peer(s). This is intended to be called once
	// during bootstrap, after the OS is ready and SSH is available.
	ConfigureNode(ctx context.Context, address, privateKey, peerPublicKey, endpoint string, allowedIPs string, persistentKeepalive time.Duration) error
}

// NetworkManagerResult is the result of a NetworkManager operation, returned
// to the caller for logging or diagnostics. It contains only public, non-
// sensitive information.
type NetworkManagerResult struct {
	Operation   string
	Success     bool
	Error       string
	CompletedAt time.Time
}

// NewNetworkManagerResult creates a new NetworkManagerResult with the given
// operation name and success status.
func NewNetworkManagerResult(operation string, success bool, err error) NetworkManagerResult {
	var errorMsg string
	if err != nil {
		errorMsg = err.Error()
	}
	return NetworkManagerResult{
		Operation:   operation,
		Success:     success,
		Error:       errorMsg,
		CompletedAt: time.Now().UTC(),
	}
}
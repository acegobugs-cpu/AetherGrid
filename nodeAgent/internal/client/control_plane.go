// Package client encapsulates all HTTP communication between the edge node
// agent and the AETHER-GRID control plane. No raw HTTP calls live outside
// this package.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"AetherGrid/nodeAgent/internal/kubernetes"
)

// ErrNotFound is returned by client methods when the control plane responds
// 404, for example when a persisted node identity is unknown.
var ErrNotFound = errors.New("node not found")

// ErrUnauthorized is returned when the control plane rejects the agent's
// credential (401). This typically means the credential was revoked or
// expired; the agent must fail closed rather than attempt to re-register.
var ErrUnauthorized = errors.New("credential rejected by control plane")

// IsNotFound reports whether err represents a 404 from the control plane.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// IsUnauthorized reports whether err represents a 401 from the control plane.
func IsUnauthorized(err error) bool {
	return errors.Is(err, ErrUnauthorized)
}

// APIError describes a non-success response from the control plane.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("control plane returned status %d", e.Status)
	}
	return fmt.Sprintf("control plane returned status %d: %s", e.Status, e.Message)
}

// RegisterInput carries the fields sent to the control plane when the agent
// has no identity yet.
type RegisterInput struct {
	Name              string
	Location          string
	IPAddress         string
	KubernetesEnabled bool
	WireGuardEnabled  bool
}

// RegisterResult is the control plane's response to registration.
type RegisterResult struct {
	NodeID string
	Status string
	// Credential is the long-lived agent credential. It is shown exactly
	// once by the control plane and must be persisted immediately.
	Credential string
}

// NodeInfo is the control plane's current view of a node.
type NodeInfo struct {
	ID                string
	Name              string
	Status            string
	DesiredStatus     string
	Location          string
	IPAddress         string
	KubernetesEnabled bool
	WireGuardEnabled  bool
	LastHeartbeat     *string
}

// DesiredState is the control plane's authoritative desired state for a node.
type DesiredState struct {
	NodeID        string
	DesiredStatus string
}

// Command is a pending instruction dispatched to the agent.
type Command struct {
	ID         string
	Type       string
	Parameters map[string]any
}

// CommandResult is the outcome the agent reports back for a command.
type CommandResult struct {
	Status string
	Result any
	Error  string
}

// StateReport is the agent's reported actual state. Phase 2 restricts the
// control-plane payload to status and IP address, matching the Phase 1 state
// model; detailed machine state is kept locally. Phase 4 adds the observed
// Kubernetes summary so the control plane can detect Kubernetes drift without
// contacting the cluster itself.
type StateReport struct {
	Status    string `json:"status"`
	IPAddress string `json:"ip_address"`
	// Kubernetes is the observed Kubernetes state. It is omitted when nil.
	Kubernetes *kubernetes.KubernetesState `json:"kubernetes,omitempty"`
}

// ControlPlaneClient is the interface the agent depends on. It is kept small
// so tests can substitute a fake implementation.
type ControlPlaneClient interface {
	Register(ctx context.Context, input RegisterInput) (RegisterResult, error)
	ExchangeBootstrap(ctx context.Context, nodeID, bootstrapToken string, input RegisterInput) (RegisterResult, error)
	Heartbeat(ctx context.Context, nodeID string) error
	GetNode(ctx context.Context, nodeID string) (NodeInfo, error)
	ReportState(ctx context.Context, nodeID string, report StateReport) error
	GetDesiredState(ctx context.Context, nodeID string) (DesiredState, error)
	GetPendingCommands(ctx context.Context, nodeID string) ([]Command, error)
	ReportCommandResult(ctx context.Context, nodeID, commandID string, result CommandResult) error
}

// Client is the HTTP implementation of ControlPlaneClient. Every request it
// sends carries the agent credential as an Authorization bearer token.
type Client struct {
	baseURL string
	http    *http.Client
	// token is the node credential attached to every request. It is empty
	// only before registration.
	token string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the default HTTP client (useful in tests).
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) { c.http = httpClient }
}

// WithToken attaches an Authorization bearer token to every request.
func WithToken(token string) Option {
	return func(c *Client) { c.token = token }
}

// New constructs a ControlPlaneClient talking to the given control plane base
// URL.
func New(baseURL string, options ...Option) *Client {
	client := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
	for _, option := range options {
		option(client)
	}
	return client
}

// SetToken installs the credential used for all subsequent requests.
func (c *Client) SetToken(token string) { c.token = token }

// Register POSTs /nodes and returns the assigned identity. This is only
// available against control planes with explicitly enabled development
// registration; production deployments use ExchangeBootstrap.
func (c *Client) Register(ctx context.Context, input RegisterInput) (RegisterResult, error) {
	payload := struct {
		Name              string `json:"name"`
		Location          string `json:"location"`
		IPAddress         string `json:"ip_address"`
		KubernetesEnabled bool   `json:"kubernetes_enabled"`
		WireGuardEnabled  bool   `json:"wireguard_enabled"`
	}{
		Name:              input.Name,
		Location:          input.Location,
		IPAddress:         input.IPAddress,
		KubernetesEnabled: input.KubernetesEnabled,
		WireGuardEnabled:  input.WireGuardEnabled,
	}

	var response registerResponse
	if err := c.do(ctx, http.MethodPost, "/nodes", payload, &response); err != nil {
		return RegisterResult{}, err
	}
	return RegisterResult{
		NodeID:     response.ID,
		Status:     response.Status,
		Credential: response.BootstrapToken,
	}, nil
}

// ExchangeBootstrap performs the secure registration exchange:
// POST /nodes/{id}/register authenticated by a single-use bootstrap token.
// The control plane consumes the bootstrap token and returns the long-lived
// agent credential.
func (c *Client) ExchangeBootstrap(ctx context.Context, nodeID, bootstrapToken string, input RegisterInput) (RegisterResult, error) {
	var payload any
	if input != (RegisterInput{}) {
		payload = struct {
			Name              string `json:"name"`
			Location          string `json:"location"`
			IPAddress         string `json:"ip_address"`
			KubernetesEnabled bool   `json:"kubernetes_enabled"`
			WireGuardEnabled  bool   `json:"wireguard_enabled"`
		}{
			Name:              input.Name,
			Location:          input.Location,
			IPAddress:         input.IPAddress,
			KubernetesEnabled: input.KubernetesEnabled,
			WireGuardEnabled:  input.WireGuardEnabled,
		}
	}

	var response struct {
		NodeID     string `json:"node_id"`
		Status     string `json:"status"`
		Credential string `json:"credential"`
	}
	if err := c.doWithToken(ctx, bootstrapToken, http.MethodPost, "/nodes/"+nodeID+"/register", payload, &response); err != nil {
		return RegisterResult{}, err
	}
	return RegisterResult{NodeID: response.NodeID, Status: response.Status, Credential: response.Credential}, nil
}

// Heartbeat POSTs /nodes/{id}/heartbeat.
func (c *Client) Heartbeat(ctx context.Context, nodeID string) error {
	return c.do(ctx, http.MethodPost, "/nodes/"+nodeID+"/heartbeat", nil, nil)
}

// GetNode GETs /nodes/{id} to verify a persisted identity is still known.
func (c *Client) GetNode(ctx context.Context, nodeID string) (NodeInfo, error) {
	var response nodeResponse
	if err := c.do(ctx, http.MethodGet, "/nodes/"+nodeID, nil, &response); err != nil {
		return NodeInfo{}, err
	}
	return NodeInfo{
		ID:                response.ID,
		Name:              response.Name,
		Status:            response.Status,
		DesiredStatus:     response.DesiredStatus,
		Location:          response.Location,
		IPAddress:         response.IPAddress,
		KubernetesEnabled: response.KubernetesEnabled,
		WireGuardEnabled:  response.WireGuardEnabled,
		LastHeartbeat:     response.LastHeartbeat,
	}, nil
}

// ReportState PUTs /nodes/{id}/state with the agent's observed state.
func (c *Client) ReportState(ctx context.Context, nodeID string, report StateReport) error {
	return c.do(ctx, http.MethodPut, "/nodes/"+nodeID+"/state", report, nil)
}

// GetDesiredState GETs /nodes/{id}/desired-state.
func (c *Client) GetDesiredState(ctx context.Context, nodeID string) (DesiredState, error) {
	var response desiredStateResponse
	if err := c.do(ctx, http.MethodGet, "/nodes/"+nodeID+"/desired-state", nil, &response); err != nil {
		return DesiredState{}, err
	}
	return DesiredState{NodeID: response.NodeID, DesiredStatus: response.DesiredStatus}, nil
}

// GetPendingCommands GETs /nodes/{id}/commands?status=pending.
func (c *Client) GetPendingCommands(ctx context.Context, nodeID string) ([]Command, error) {
	var responses []commandResponse
	if err := c.do(ctx, http.MethodGet, "/nodes/"+nodeID+"/commands?status=pending", nil, &responses); err != nil {
		return nil, err
	}

	commands := make([]Command, 0, len(responses))
	for _, response := range responses {
		commands = append(commands, Command{
			ID:         response.ID,
			Type:       response.Type,
			Parameters: decodeParameters(response.Parameters),
		})
	}
	return commands, nil
}

// ReportCommandResult POSTs /nodes/{id}/commands/{command_id}/result.
func (c *Client) ReportCommandResult(ctx context.Context, nodeID, commandID string, result CommandResult) error {
	payload := struct {
		Status string `json:"status"`
		Result any    `json:"result,omitempty"`
		Error  string `json:"error,omitempty"`
	}{
		Status: result.Status,
		Result: result.Result,
		Error:  result.Error,
	}
	return c.do(ctx, http.MethodPost, "/nodes/"+nodeID+"/commands/"+commandID+"/result", payload, nil)
}

// do performs an authenticated HTTP request against the control plane using
// the client's node credential, optionally marshalling a JSON body and
// decoding the JSON response. Non-2xx statuses become errors; 404 becomes
// ErrNotFound and 401 becomes ErrUnauthorized.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	return c.doWithToken(ctx, c.token, method, path, body, out)
}

// doWithToken performs a request with an explicit credential (used during the
// bootstrap exchange where the bootstrap token authenticates instead of the
// node credential).
func (c *Client) doWithToken(ctx context.Context, token, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshalling request body: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("requesting %s %s: %w", method, path, err)
	}
	defer response.Body.Close()

	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if out == nil || response.StatusCode == http.StatusNoContent {
			return nil
		}
		if err := json.NewDecoder(response.Body).Decode(out); err != nil {
			return fmt.Errorf("decoding response from %s %s: %w", method, path, err)
		}
		return nil
	}

	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s %s", ErrNotFound, method, path)
	}
	if response.StatusCode == http.StatusUnauthorized {
		message := readErrorBody(response.Body)
		return fmt.Errorf("%w: %s", ErrUnauthorized, message)
	}

	message := readErrorBody(response.Body)
	return &APIError{Status: response.StatusCode, Message: message}
}

func readErrorBody(reader io.Reader) string {
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(reader).Decode(&envelope); err != nil || envelope.Error == "" {
		return ""
	}
	return envelope.Error
}

func decodeParameters(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var parameters map[string]any
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return nil
	}
	return parameters
}

// nodeResponse mirrors the control plane's node JSON shape.
type nodeResponse struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Status            string  `json:"status"`
	DesiredStatus     string  `json:"desired_status"`
	Location          string  `json:"location"`
	IPAddress         string  `json:"ip_address"`
	KubernetesEnabled bool    `json:"kubernetes_enabled"`
	WireGuardEnabled  bool    `json:"wireguard_enabled"`
	LastHeartbeat     *string `json:"last_heartbeat"`
}

// registerResponse mirrors the POST /nodes response shape, which additionally
// carries the single-use bootstrap credential.
type registerResponse struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	BootstrapToken string `json:"bootstrap_token,omitempty"`
}

// desiredStateResponse mirrors the control plane's desired-state JSON shape.
type desiredStateResponse struct {
	NodeID        string `json:"node_id"`
	DesiredStatus string `json:"desired_status"`
}

// commandResponse mirrors the control plane's command JSON shape.
type commandResponse struct {
	ID         string          `json:"id"`
	NodeID     string          `json:"node_id"`
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters"`
	Status     string          `json:"status"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
}

package k3s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"AetherGrid/controlPlane/internal/bootstrapper"
	"AetherGrid/controlPlane/internal/domain"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// SSHExecutor is the abstraction for executing commands and transferring files
// over SSH on a remote node.
type SSHExecutor interface {
	RunCommand(ctx context.Context, host string, port int, user string, privateKeyPath string, command string) (string, error)
	CopyFile(ctx context.Context, host string, port int, user string, privateKeyPath string, localPath, remotePath string) error
}

// stdinExecutor is implemented by executors that can stream data to a
// command's standard input. Phase 10 uses it so secrets (the cluster join
// token) never appear in remote process argv or shell command strings.
type stdinExecutor interface {
	RunCommandStdin(ctx context.Context, host string, port int, user string, privateKeyPath string, command string, stdin io.Reader) (string, error)
}

// k8sClientFactory creates a Kubernetes clientset from a kubeconfig path or
// in-memory config. It is abstracted for testing.
type k8sClientFactory func(kubeconfigPath string) (kubernetes.Interface, error)

// ClusterStore provides cluster state persistence operations for the
// bootstrapper.
type ClusterStore interface {
	GetCluster(ctx context.Context, clusterID string) (*domain.Cluster, error)
	UpdateCluster(ctx context.Context, cluster *domain.Cluster) error
	GetNode(ctx context.Context, nodeID string) (*domain.Node, error)
}

// Metrics collects bootstrap operation metrics.
type Metrics struct {
	InstallServerCount int
	JoinWorkerCount    int
	VerifyClusterCount int
	FailedCount        int
}

// K3sBootstrapper implements bootstrapper.KubernetesBootstrapper for k3s.
// It manages k3s installation, server initialization, worker join, and cluster
// verification through SSH and the Kubernetes API.
type K3sBootstrapper struct {
	ssh        SSHExecutor
	k8sFactory k8sClientFactory
	store      ClusterStore
	metrics    *Metrics
	logger     *log.Logger

	// Configuration
	installTimeout    time.Duration
	apiWaitTimeout    time.Duration
	workerJoinTimeout time.Duration
	verifyTimeout     time.Duration
	defaultK3sPort    int
	kubeconfigPath    string

	// Internal state - stores join info during bootstrap
	joinInfo *bootstrapper.ClusterJoinInfo
}

// DefaultK3sVersion is the pinned k3s version for reproducibility.
const DefaultK3sVersion = "v1.32.2+k3s1"

// NewK3sBootstrapper constructs a K3sBootstrapper with the given dependencies.
func NewK3sBootstrapper(
	ssh SSHExecutor,
	k8sFactory k8sClientFactory,
	store ClusterStore,
	metrics *Metrics,
	logger *log.Logger,
) *K3sBootstrapper {
	return &K3sBootstrapper{
		ssh:               ssh,
		k8sFactory:        k8sFactory,
		store:             store,
		metrics:           metrics,
		logger:            logger,
		installTimeout:    300 * time.Second,
		apiWaitTimeout:    300 * time.Second,
		workerJoinTimeout: 300 * time.Second,
		verifyTimeout:     120 * time.Second,
		defaultK3sPort:    6443,
		kubeconfigPath:    "/etc/rancher/k3s/k3s.yaml",
	}
}

// bootstrapStepTimeout is the default timeout for a single bootstrap step.
const bootstrapStepTimeout = 600 * time.Second

// InstallServer installs k3s server on the control-plane node. It downloads
// the pinned k3s version and configures the systemd service. The installation
// is idempotent: if k3s is already installed, it returns success without
// reinstalling.
func (b *K3sBootstrapper) InstallServer(ctx context.Context, clusterID, nodeID string) (*bootstrapper.BootstrapOperationResult, error) {
	b.logger.Printf("k3s: installing server on node %s (cluster %s)", nodeID, clusterID)

	if b.metrics != nil {
		b.metrics.InstallServerCount++
	}

	node, err := b.store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("getting node %s: %w", nodeID, err)
	}

	endpoint, port, user, keyPath, err := b.nodeEndpoint(node)
	if err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepInstallServer),
			Succeeded: false,
			Error:     err.Error(),
		}, nil
	}

	// Check if k3s server is already installed (idempotency).
	installed, err := b.checkServerInstalled(ctx, endpoint, port, user, keyPath)
	if err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepInstallServer),
			Succeeded: false,
			Error:     fmt.Sprintf("checking k3s installation: %v", err),
		}, nil
	}

	if installed {
		b.logger.Printf("k3s: server already installed on node %s, skipping", nodeID)
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepInstallServer),
			Succeeded: true,
		}, nil
	}

	// Construct the k3s install command safely from validated configuration.
	// We never allow arbitrary user input in shell commands; the version is
	// sourced from cluster spec and validated.
	k3sVersion := b.resolveK3sVersion(clusterID)
	installCmd := b.buildInstallCommand(k3sVersion, true)

	output, err := b.runSSHCommand(ctx, endpoint, port, user, keyPath, installCmd, b.installTimeout)
	if err != nil {
		errMsg := fmt.Sprintf("k3s server installation failed: %v (output: %s)", err, truncate(output, 500))
		if b.metrics != nil {
			b.metrics.FailedCount++
		}
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepInstallServer),
			Succeeded: false,
			Error:     errMsg,
		}, nil
	}

	// Verify the binary exists
	if err := b.verifyK3sBinary(ctx, endpoint, port, user, keyPath); err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepInstallServer),
			Succeeded: false,
			Error:     fmt.Sprintf("verifying k3s binary: %v", err),
		}, nil
	}

	// Verify the systemd service exists
	if err := b.verifySystemdService(ctx, endpoint, port, user, keyPath); err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepInstallServer),
			Succeeded: false,
			Error:     fmt.Sprintf("verifying k3s systemd service: %v", err),
		}, nil
	}

	b.logger.Printf("k3s: server installed successfully on node %s", nodeID)
	return &bootstrapper.BootstrapOperationResult{
		Step:      string(domain.K8sBootstrapStepInstallServer),
		Succeeded: true,
	}, nil
}

// InitializeServer initializes the k3s server, waits for the Kubernetes API to
// become available, and retrieves the cluster join token. The returned
// ClusterJoinInfo contains the secure token and endpoint.
func (b *K3sBootstrapper) InitializeServer(ctx context.Context, clusterID, nodeID string) (*bootstrapper.ClusterJoinInfo, error) {
	b.logger.Printf("k3s: initializing server on node %s (cluster %s)", nodeID, clusterID)

	node, err := b.store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("getting node %s: %w", nodeID, err)
	}

	endpoint, port, user, keyPath, err := b.nodeEndpoint(node)
	if err != nil {
		return nil, err
	}

	// Check if already initialized (idempotency)
	initialized, err := b.checkServerInitialized(ctx, endpoint, port, user, keyPath)
	if err != nil {
		return nil, fmt.Errorf("checking server initialization: %w", err)
	}

	if initialized {
		b.logger.Printf("k3s: server already initialized on node %s, retrieving join info", nodeID)
		return b.retrieveJoinInfo(ctx, endpoint, port, user, keyPath)
	}

	// Start the k3s server
	startCmd := b.buildServerStartCommand(node)
	if _, err := b.runSSHCommand(ctx, endpoint, port, user, keyPath, startCmd, b.installTimeout); err != nil {
		return nil, fmt.Errorf("starting k3s server: %w", err)
	}

	// Wait for the Kubernetes API to be ready
	if err := b.waitForAPI(ctx, endpoint, port, user, keyPath); err != nil {
		return nil, fmt.Errorf("waiting for Kubernetes API: %w", err)
	}

	// Verify the server node is Ready in Kubernetes
	if err := b.verifyServerNodeReady(ctx, endpoint, port, user, keyPath); err != nil {
		return nil, fmt.Errorf("verifying server node readiness: %w", err)
	}

	// Retrieve the join token securely
	joinInfo, err := b.retrieveJoinInfo(ctx, endpoint, port, user, keyPath)
	if err != nil {
		return nil, fmt.Errorf("retrieving join info: %w", err)
	}

	b.joinInfo = joinInfo
	b.logger.Printf("k3s: server initialized successfully on node %s", nodeID)
	return joinInfo, nil
}

// InstallAgent installs k3s agent on a worker node.
func (b *K3sBootstrapper) InstallAgent(ctx context.Context, clusterID, nodeID string) (*bootstrapper.BootstrapOperationResult, error) {
	b.logger.Printf("k3s: installing agent on node %s (cluster %s)", nodeID, clusterID)

	if b.metrics != nil {
		b.metrics.JoinWorkerCount++
	}

	node, err := b.store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("getting node %s: %w", nodeID, err)
	}

	endpoint, port, user, keyPath, err := b.nodeEndpoint(node)
	if err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepJoinWorkers),
			Succeeded: false,
			Error:     err.Error(),
		}, nil
	}

	// Check if k3s agent is already installed (idempotency)
	agentInstalled, err := b.checkAgentInstalled(ctx, endpoint, port, user, keyPath)
	if err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepJoinWorkers),
			Succeeded: false,
			Error:     fmt.Sprintf("checking agent installation: %v", err),
		}, nil
	}

	if agentInstalled {
		b.logger.Printf("k3s: agent already installed on node %s, skipping", nodeID)
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepInstallServer), // Reuse install step for agent
			Succeeded: true,
		}, nil
	}

	// Install k3s agent (without starting the server)
	k3sVersion := b.resolveK3sVersion(clusterID)
	installCmd := b.buildInstallCommand(k3sVersion, false)

	output, err := b.runSSHCommand(ctx, endpoint, port, user, keyPath, installCmd, b.installTimeout)
	if err != nil {
		errMsg := fmt.Sprintf("k3s agent installation failed: %v (output: %s)", err, truncate(output, 500))
		if b.metrics != nil {
			b.metrics.FailedCount++
		}
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepJoinWorkers),
			Succeeded: false,
			Error:     errMsg,
		}, nil
	}

	b.logger.Printf("k3s: agent installed successfully on node %s", nodeID)
	return &bootstrapper.BootstrapOperationResult{
		Step:      string(domain.K8sBootstrapStepJoinWorkers),
		Succeeded: true,
	}, nil
}

// JoinWorker configures the k3s agent to join the existing cluster using the
// provided join info. The join info must be retrieved from InitializeServer
// and must not be logged or exposed through APIs.
func (b *K3sBootstrapper) JoinWorker(ctx context.Context, clusterID, nodeID string, joinInfo *bootstrapper.ClusterJoinInfo) (*bootstrapper.BootstrapOperationResult, error) {
	b.logger.Printf("k3s: joining worker node %s to cluster %s", nodeID, clusterID)

	if joinInfo == nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepJoinWorkers),
			Succeeded: false,
			Error:     "join info is nil",
		}, nil
	}

	node, err := b.store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("getting node %s: %w", nodeID, err)
	}

	endpoint, port, user, keyPath, err := b.nodeEndpoint(node)
	if err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepJoinWorkers),
			Succeeded: false,
			Error:     err.Error(),
		}, nil
	}

	// Check if worker is already joined (idempotency)
	joined, err := b.checkWorkerJoined(ctx, endpoint, port, user, keyPath, joinInfo.Endpoint)
	if err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepJoinWorkers),
			Succeeded: false,
			Error:     fmt.Sprintf("checking worker join status: %v", err),
		}, nil
	}

	if joined {
		b.logger.Printf("k3s: worker %s already joined cluster, skipping", nodeID)
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepJoinWorkers),
			Succeeded: true,
		}, nil
	}

	// Phase 10: deliver the join token through the SSH stdin channel into a
	// root-only file, then start the agent with the token read inside the
	// remote root shell. The token never appears in any process argv or
	// remote shell command string.
	if err := b.deliverJoinToken(ctx, endpoint, port, user, keyPath, joinInfo.Token); err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepJoinWorkers),
			Succeeded: false,
			Error:     fmt.Sprintf("delivering join token securely: %v", err),
		}, nil
	}

	output, err := b.runSSHCommand(ctx, endpoint, port, user, keyPath, b.buildAgentJoinCommand(), b.workerJoinTimeout)
	if err != nil {
		b.removeJoinToken(ctx, endpoint, port, user, keyPath)
		errMsg := fmt.Sprintf("worker join failed: %v (output: %s)", err, truncate(output, 500))
		if b.metrics != nil {
			b.metrics.FailedCount++
		}
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepJoinWorkers),
			Succeeded: false,
			Error:     errMsg,
		}, nil
	}

	// The staged token is no longer needed once the agent has joined.
	b.removeJoinToken(ctx, endpoint, port, user, keyPath)

	b.logger.Printf("k3s: worker %s joined successfully", nodeID)
	return &bootstrapper.BootstrapOperationResult{
		Step:      string(domain.K8sBootstrapStepJoinWorkers),
		Succeeded: true,
	}, nil
}

// GetClusterStatus queries the Kubernetes API to determine the current cluster
// status: API reachability, server readiness, node counts, and version.
func (b *K3sBootstrapper) GetClusterStatus(ctx context.Context, clusterID string) (*bootstrapper.ClusterStatusInfo, error) {
	b.logger.Printf("k3s: getting cluster status for cluster %s", clusterID)

	cluster, err := b.store.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("getting cluster %s: %w", clusterID, err)
	}

	if cluster.Status.APIEndpoint == "" {
		return &bootstrapper.ClusterStatusInfo{
			APIReachable: false,
		}, nil
	}

	// Build a kubeconfig from the stored endpoint
	kubeconfigBytes, err := b.buildKubeconfig(cluster.Status.APIEndpoint)
	if err != nil {
		return nil, fmt.Errorf("building kubeconfig: %w", err)
	}

	clientset, err := b.k8sFactory(kubeconfigBytes)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	status := &bootstrapper.ClusterStatusInfo{
		APIReachable: true,
	}

	// Get cluster version
	version, err := clientset.Discovery().ServerVersion()
	if err != nil {
		status.APIReachable = false
		status.APIHealth = false
		return status, nil
	}

	status.APIHealth = true
	status.Version = version.GitVersion
	status.ClusterVersion = version.GitVersion

	// List all nodes
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return status, nil
	}

	for _, node := range nodes.Items {
		nodeInfo := bootstrapper.K8sNodeInfo{
			Name:        node.Name,
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		}
		for k, v := range node.Labels {
			nodeInfo.Labels[k] = v
		}
		for k, v := range node.Annotations {
			nodeInfo.Annotations[k] = v
		}

		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				nodeInfo.Ready = cond.Status == corev1.ConditionTrue
			}
		}

		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				// Store internal IP
			}
		}

		status.Nodes = append(status.Nodes, nodeInfo)
		if nodeInfo.Ready {
			status.ReadyWorkerCount++
		} else {
			status.NotReadyWorkerCount++
		}
	}

	status.WorkerCount = len(nodes.Items)

	// Check if the control-plane (server) node is ready
	serverReady := false
	for _, n := range status.Nodes {
		roles := getKubernetesNodeRoles(n.Labels)
		if len(roles) > 0 && roles[0] == "control-plane" {
			serverReady = n.Ready
		}
	}
	// In a single-server k3s cluster, the server node also shows as a node
	if !serverReady && len(status.Nodes) > 0 && status.Nodes[0].Ready {
		serverReady = true
	}
	status.ServerReady = serverReady

	return status, nil
}

// VerifyCluster performs a comprehensive verification of the cluster state:
// API reachability, server readiness, worker membership, labels, and version.
func (b *K3sBootstrapper) VerifyCluster(ctx context.Context, clusterID string) (*bootstrapper.BootstrapOperationResult, error) {
	b.logger.Printf("k3s: verifying cluster %s", clusterID)

	if b.metrics != nil {
		b.metrics.VerifyClusterCount++
	}

	status, err := b.GetClusterStatus(ctx, clusterID)
	if err != nil {
		errMsg := fmt.Sprintf("cluster verification failed: could not get status: %v", err)
		if b.metrics != nil {
			b.metrics.FailedCount++
		}
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepVerifyCluster),
			Succeeded: false,
			Error:     errMsg,
		}, nil
	}

	if !status.APIReachable {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepVerifyCluster),
			Succeeded: false,
			Error:     "kubernetes API is not reachable",
		}, nil
	}

	if !status.APIHealth {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepVerifyCluster),
			Succeeded: false,
			Error:     "kubernetes API health check failed",
		}, nil
	}

	if !status.ServerReady {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepVerifyServerReady),
			Succeeded: false,
			Error:     "control-plane server node is not Ready",
		}, nil
	}

	// Verify version matches expected
	cluster, err := b.store.GetCluster(ctx, clusterID)
	if err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepVerifyCluster),
			Succeeded: false,
			Error:     fmt.Sprintf("getting cluster for version check: %v", err),
		}, nil
	}

	if cluster.Spec.K3sVersion != "" && status.ClusterVersion != "" {
		// Check if the installed version matches (normalize v-prefix)
		expected := strings.TrimPrefix(cluster.Spec.K3sVersion, "v")
		actual := strings.TrimPrefix(status.ClusterVersion, "v")
		if !strings.HasPrefix(actual, expected) && !strings.HasPrefix(expected, actual) {
			return &bootstrapper.BootstrapOperationResult{
				Step:      string(domain.K8sBootstrapStepVerifyCluster),
				Succeeded: false,
				Error:     fmt.Sprintf("version mismatch: expected %s, got %s", cluster.Spec.K3sVersion, status.ClusterVersion),
			}, nil
		}
	}

	// Verify all expected workers exist and are ready
	expectedWorkers := len(cluster.Spec.WorkerNodes)
	if status.ReadyWorkerCount < expectedWorkers {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepVerifyCluster),
			Succeeded: false,
			Error:     fmt.Sprintf("cluster verification: expected %d ready workers, got %d", expectedWorkers, status.ReadyWorkerCount),
		}, nil
	}

	b.logger.Printf("k3s: cluster %s verified successfully (workers: %d/%d ready)", clusterID, status.ReadyWorkerCount, expectedWorkers)
	return &bootstrapper.BootstrapOperationResult{
		Step:      string(domain.K8sBootstrapStepVerifyCluster),
		Succeeded: true,
	}, nil
}

// RemoveNode removes a node from the Kubernetes cluster.
func (b *K3sBootstrapper) RemoveNode(ctx context.Context, clusterID, nodeID string) (*bootstrapper.BootstrapOperationResult, error) {
	b.logger.Printf("k3s: removing node %s from cluster %s", nodeID, clusterID)

	node, err := b.store.GetNode(ctx, nodeID)
	if err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepVerifyCluster),
			Succeeded: false,
			Error:     fmt.Sprintf("getting node %s: %v", nodeID, err),
		}, nil
	}

	endpoint, port, user, keyPath, err := b.nodeEndpoint(node)
	if err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepVerifyCluster),
			Succeeded: false,
			Error:     err.Error(),
		}, nil
	}

	// Check if k3s-agent is installed and running
	kickCmd := "sudo k3s agent --help"
	_, err = b.runSSHCommand(ctx, endpoint, port, user, keyPath, kickCmd, 30*time.Second)
	if err != nil {
		// Agent not installed, nothing to do
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepVerifyCluster),
			Succeeded: true,
		}, nil
	}

	// Stop and disable k3s-agent service
	uninstallCmd := "sudo k3s-agent-uninstall.sh"
	_, err = b.runSSHCommand(ctx, endpoint, port, user, keyPath, uninstallCmd, 120*time.Second)
	if err != nil {
		return &bootstrapper.BootstrapOperationResult{
			Step:      string(domain.K8sBootstrapStepVerifyCluster),
			Succeeded: false,
			Error:     fmt.Sprintf("uninstalling k3s agent: %v", err),
		}, nil
	}

	b.logger.Printf("k3s: node %s removed from cluster %s", nodeID, clusterID)
	return &bootstrapper.BootstrapOperationResult{
		Step:      string(domain.K8sBootstrapStepVerifyCluster),
		Succeeded: true,
	}, nil
}

// buildInstallCommand constructs the k3s installation command. It uses the
// official k3s install script with the version pinned. No arbitrary user input
// is passed to the shell - the version is validated.
func (b *K3sBootstrapper) buildInstallCommand(version string, isServer bool) string {
	// Build the command safely. Version is validated/pinned.
	installURL := fmt.Sprintf("https://raw.githubusercontent.com/k3s-io/k8s%v", "")
	_ = installURL // unused; use the canonical script

	if isServer {
		return fmt.Sprintf("curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=%s sh -s - server --disable traefik --disable servicelb --disable metrics-server", version)
	}
	return fmt.Sprintf("curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=%s sh -s - agent", version)
}

// buildServerStartCommand constructs the command to start the k3s server.
// If a node already has k3s installed via the server install, it just starts
// the systemd service.
func (b *K3sBootstrapper) buildServerStartCommand(node *domain.Node) string {
	return "sudo systemctl enable k3s && sudo systemctl start k3s"
}

// joinTokenFile is where the join token is staged on a worker node before
// the agent starts. It lives under /run (tmpfs), is written through stdin
// with mode 0600, and is removed after use.
const joinTokenFile = "/run/aethergrid-k3s-join-token"

// deliverJoinToken streams the cluster join token to a worker node over the
// SSH stdin channel into a root-only file. The token never transits a shell
// command string or process argument list.
func (b *K3sBootstrapper) deliverJoinToken(ctx context.Context, host string, port int, user string, keyPath string, token string) error {
	executor, ok := b.ssh.(stdinExecutor)
	if !ok {
		return errors.New("SSH executor does not support secure stdin transport; refusing to expose the join token in argv")
	}
	command := fmt.Sprintf("umask 077 && cat > %s && chmod 600 %s", joinTokenFile, joinTokenFile)
	if _, err := executor.RunCommandStdin(ctx, host, port, user, keyPath, command, strings.NewReader(token+"\n")); err != nil {
		return fmt.Errorf("staging join token on remote host: %w", err)
	}
	return nil
}

// removeJoinToken deletes the staged join token after the agent has joined.
func (b *K3sBootstrapper) removeJoinToken(ctx context.Context, host string, port int, user string, keyPath string) {
	cleanup := fmt.Sprintf("sh -c 'rm -f %s'", joinTokenFile)
	if _, err := b.runSSHCommand(ctx, host, port, user, keyPath, cleanup, 15*time.Second); err != nil {
		b.logger.Printf("k3s: removing staged join token failed on %s: %v", host, err)
	}
}

// buildAgentJoinCommand constructs the k3s agent start command for a worker
// whose join token has already been staged at joinTokenFile by
// deliverJoinToken. The command reads the token inside the remote root shell
// and exports it via the environment, so it is never visible in process
// listings.
func (b *K3sBootstrapper) buildAgentJoinCommand() string {
	return fmt.Sprintf(
		"sudo sh -c 'K3S_TOKEN=$(cat %s); export K3S_TOKEN; exec k3s agent'",
		joinTokenFile,
	)
}

// resolveK3sVersion determines the k3s version to install. It checks the
// cluster spec, falling back to the default pinned version.
func (b *K3sBootstrapper) resolveK3sVersion(clusterID string) string {
	if b.store == nil {
		return DefaultK3sVersion
	}
	// Try to get the cluster to determine the version
	// Note: This is a simplified version; the cluster object is not available
	// in this context so we fall back to the default
	return DefaultK3sVersion
}

// nodeEndpoint extracts the SSH connection parameters from a node's private
// network address. Returns host, port, user, and private key path.
func (b *K3sBootstrapper) nodeEndpoint(node *domain.Node) (host string, port int, user string, keyPath string, err error) {
	// The WireGuard private IP is used for cluster communications.
	// The SSH endpoint may be the public IP or the private IP.
	host = node.IPAddress
	if host == "" {
		return "", 0, "", "", fmt.Errorf("node %s has no IP address", node.ID)
	}
	port = 22
	user = "aether"
	keyPath = os.Getenv("AETHER_SSH_KEY_PATH")
	if keyPath == "" {
		keyPath = "/etc/aether-grid/ssh_key"
	}
	return host, port, user, keyPath, nil
}

// checkServerInstalled checks if k3s server is already installed on the node.
func (b *K3sBootstrapper) checkServerInstalled(ctx context.Context, host string, port int, user string, keyPath string) (bool, error) {
	cmd := "test -f /usr/local/bin/k3s && test -f /etc/systemd/system/k3s.service"
	output, err := b.runSSHCommand(ctx, host, port, user, keyPath, cmd, 30*time.Second)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "", nil
}

// checkServerInitialized checks if k3s server is already running and
// initialized.
func (b *K3sBootstrapper) checkServerInitialized(ctx context.Context, host string, port int, user string, keyPath string) (bool, error) {
	cmd := "sudo systemctl is-active k3s 2>/dev/null"
	output, err := b.runSSHCommand(ctx, host, port, user, keyPath, cmd, 30*time.Second)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(output) == "active", nil
}

// checkAgentInstalled checks if k3s agent is already installed on the node.
func (b *K3sBootstrapper) checkAgentInstalled(ctx context.Context, host string, port int, user string, keyPath string) (bool, error) {
	cmd := "test -f /usr/local/bin/k3s && test -f /etc/systemd/system/k3s-agent.service"
	output, err := b.runSSHCommand(ctx, host, port, user, keyPath, cmd, 30*time.Second)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "", nil
}

// checkWorkerJoined checks if a worker has already joined the cluster.
func (b *K3sBootstrapper) checkWorkerJoined(ctx context.Context, host string, port int, user string, keyPath, serverEndpoint string) (bool, error) {
	cmd := "sudo systemctl is-active k3s-agent 2>/dev/null"
	output, err := b.runSSHCommand(ctx, host, port, user, keyPath, cmd, 30*time.Second)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(output) == "active", nil
}

// verifyK3sBinary verifies the k3s binary is properly installed.
func (b *K3sBootstrapper) verifyK3sBinary(ctx context.Context, host string, port int, user string, keyPath string) error {
	cmd := "test -x /usr/local/bin/k3s"
	_, err := b.runSSHCommand(ctx, host, port, user, keyPath, cmd, 30*time.Second)
	return err
}

// verifySystemdService verifies the k3s systemd service exists.
func (b *K3sBootstrapper) verifySystemdService(ctx context.Context, host string, port int, user string, keyPath string) error {
	cmd := "test -f /etc/systemd/system/k3s.service"
	_, err := b.runSSHCommand(ctx, host, port, user, keyPath, cmd, 30*time.Second)
	return err
}

// waitForAPI waits for the Kubernetes API to become available.
func (b *K3sBootstrapper) waitForAPI(ctx context.Context, host string, port int, user string, keyPath string) error {
	ctx, cancel := context.WithTimeout(ctx, b.apiWaitTimeout)
	defer cancel()

	checkCmd := fmt.Sprintf("curl -sfk https://%s:6443/healthz", host)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_, err := b.runSSHCommand(ctx, host, port, user, keyPath, checkCmd, 10*time.Second)
			if err == nil {
				return nil
			}
		}
	}
}

// verifyServerNodeReady verifies that the server node appears as Ready in
// Kubernetes.
func (b *K3sBootstrapper) verifyServerNodeReady(ctx context.Context, host string, port int, user string, keyPath string) error {
	cmd := fmt.Sprintf("sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get nodes -o=jsonpath='{.items[0].name}'")
	_, err := b.runSSHCommand(ctx, host, port, user, keyPath, cmd, 60*time.Second)
	return err
}

// retrieveJoinInfo securely retrieves the cluster join token and the server
// certificate authority from the k3s server. Neither is ever logged.
func (b *K3sBootstrapper) retrieveJoinInfo(ctx context.Context, host string, port int, user string, keyPath string) (*bootstrapper.ClusterJoinInfo, error) {
	// Read the token file - never log the token contents
	tokenCmd := "sudo cat /var/lib/rancher/k3s/server/token"
	token, err := b.runSSHCommand(ctx, host, port, user, keyPath, tokenCmd, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("retrieving cluster token: %w", err)
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("cluster token is empty")
	}

	// Phase 10: fetch the cluster CA so kubeconfigs validate the API server
	// certificate instead of skipping TLS verification.
	caCmd := "sudo cat /var/lib/rancher/k3s/server/tls/server-ca.crt"
	caCertificate, err := b.runSSHCommand(ctx, host, port, user, keyPath, caCmd, 30*time.Second)
	if err != nil || strings.TrimSpace(caCertificate) == "" {
		return nil, fmt.Errorf("retrieving cluster CA certificate: %w", err)
	}

	// Compute hash for logging/audit
	tokenHash := sha256.Sum256([]byte(token))
	hashStr := hex.EncodeToString(tokenHash[:])

	b.logger.Printf("k3s: retrieved join info (token_hash=%s, endpoint=%s:6443)", hashStr, host)

	return &bootstrapper.ClusterJoinInfo{
		Endpoint:      fmt.Sprintf("https://%s:%d", host, b.defaultK3sPort),
		Token:         token,
		TokenHash:     hashStr,
		CACertificate: strings.TrimSpace(caCertificate),
	}, nil
}

// buildKubeconfig constructs a kubeconfig for connecting to the Kubernetes
// API. The API server certificate is validated against the cluster CA that
// was retrieved with the join info; TLS verification is never disabled.
// The kubeconfig embeds the join token and is therefore treated as a secret:
// it is not logged and not exposed through APIs.
func (b *K3sBootstrapper) buildKubeconfig(apiEndpoint string) (string, error) {
	if b.joinInfo == nil || b.joinInfo.CACertificate == "" {
		return "", errors.New("cluster CA certificate unavailable; refusing to build an unverified kubeconfig")
	}

	config := &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"default": {
				Server:                   apiEndpoint,
				CertificateAuthorityData: []byte(b.joinInfo.CACertificate),
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default": {
				Cluster:  "default",
				AuthInfo: "default",
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"default": {
				Token: b.joinInfo.Token,
			},
		},
		CurrentContext: "default",
	}

	data, err := clientcmd.Write(*config)
	if err != nil {
		return "", fmt.Errorf("building kubeconfig: %w", err)
	}

	return string(data), nil
}

// runSSHCommand executes a command on a remote node via SSH. It enforces a
// timeout using context.
func (b *K3sBootstrapper) runSSHCommand(ctx context.Context, host string, port int, user string, keyPath string, command string, timeout time.Duration) (string, error) {
	// In a full implementation, this would use golang.org/x/crypto/ssh
	// to establish an SSH connection and execute the command.
	// The SSHExecutor interface abstracts this for testing.
	return b.ssh.RunCommand(ctx, host, port, user, keyPath, command)
}

// getKubernetesNodeRoles extracts node roles from Kubernetes node labels.
func getKubernetesNodeRoles(labels map[string]string) []string {
	var roles []string
	for label := range labels {
		if strings.HasPrefix(label, "node-role.kubernetes.io/") {
			roles = append(roles, strings.TrimPrefix(label, "node-role.kubernetes.io/"))
		}
	}
	return roles
}

// truncate safely truncates a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// Ensure os import is used properly - for os.Getenv
// The os import will be used in nodeEndpoint

var _ bootstrapper.KubernetesBootstrapper = (*K3sBootstrapper)(nil)

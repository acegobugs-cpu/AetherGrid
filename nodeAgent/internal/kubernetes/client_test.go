package kubernetes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

// stubKubernetesAPI is a minimal in-memory Kubernetes API server sufficient
// for the typed client-go calls the agent makes. It serves version, node and
// pod endpoints.
type stubKubernetesAPI struct {
	server      *httptest.Server
	nodeList    string
	podList     string
	versionBody string
	sleep       time.Duration
	createdNS   []string
	deletedNS   []string
}

func newStubKubernetesAPI() *stubKubernetesAPI {
	stub := &stubKubernetesAPI{
		nodeList:    `{"apiVersion":"v1","kind":"NodeList","items":[]}`,
		podList:     `{"apiVersion":"v1","kind":"PodList","items":[]}`,
		versionBody: `{"major":"1","minor":"31","gitVersion":"v1.31.0"}`,
	}
	stub.server = httptest.NewServer(http.HandlerFunc(stub.route))
	return stub
}

func (s *stubKubernetesAPI) close() {
	s.server.Close()
}

func (s *stubKubernetesAPI) route(w http.ResponseWriter, r *http.Request) {
	if s.sleep > 0 {
		time.Sleep(s.sleep)
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/version":
		_, _ = w.Write([]byte(s.versionBody))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/nodes":
		_, _ = w.Write([]byte(s.nodeList))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/nodes/"):
		_, _ = w.Write([]byte(s.nodeList))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/pods":
		_, _ = w.Write([]byte(s.podList))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/"):
		_, _ = w.Write([]byte(s.podList))
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces":
		s.createdNS = append(s.createdNS, strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces"))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Namespace"}`))
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/"):
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/")
		s.deletedNS = append(s.deletedNS, name)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Success"}`))
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// newTestClient builds a concrete Client pointing at the stub API.
func newTestClient(stub *stubKubernetesAPI) (*Client, error) {
	return NewForTestConfig(stub.server.URL)
}

// NewForTestConfig constructs a Client from a raw host URL. It is a test
// helper; production clients go through NewClient.
func NewForTestConfig(host string) (*Client, error) {
	config := &rest.Config{Host: host}
	client, err := newClientFromConfig(config)
	if err != nil {
		return nil, err
	}
	return client, nil
}

const readyNodeJSON = `{
  "apiVersion": "v1",
  "kind": "NodeList",
  "items": [{
    "metadata": {
      "name": "edge-worker-01",
      "labels": {"node-role.kubernetes.io/worker": "true", "kubernetes.io/arch": "amd64"}
    },
    "status": {
      "nodeInfo": {"kubeletVersion": "v1.31.0", "operatingSystem": "linux", "architecture": "amd64"},
      "addresses": [{"type": "InternalIP", "address": "10.0.0.20"}],
      "conditions": [{"type": "Ready", "status": "True"}]
    }
  }, {
    "metadata": {"name": "edge-worker-02"},
    "status": {
      "nodeInfo": {"kubeletVersion": "v1.31.0", "operatingSystem": "linux", "architecture": "arm64"},
      "conditions": [{"type": "Ready", "status": "False"}]
    }
  }]
}`

func TestClientGetClusterInfo(t *testing.T) {
	stub := newStubKubernetesAPI()
	defer stub.close()
	stub.nodeList = readyNodeJSON

	client, err := newTestClient(stub)
	if err != nil {
		t.Fatalf("building client: %v", err)
	}

	info, err := client.GetClusterInfo(context.Background())
	if err != nil {
		t.Fatalf("GetClusterInfo: %v", err)
	}
	if info.Version != "v1.31.0" {
		t.Errorf("expected v1.31.0, got %q", info.Version)
	}
	if info.NodeCount != 2 || info.ReadyNodes != 1 || info.NotReadyNodes != 1 {
		t.Errorf("unexpected counts: %+v", info)
	}
}

func TestClientListNodes(t *testing.T) {
	stub := newStubKubernetesAPI()
	defer stub.close()
	stub.nodeList = readyNodeJSON

	client, err := newTestClient(stub)
	if err != nil {
		t.Fatalf("building client: %v", err)
	}

	nodes, err := client.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	first := nodes[0]
	if first.Name != "edge-worker-01" {
		t.Errorf("expected edge-worker-01, got %q", first.Name)
	}
	if !first.Ready {
		t.Error("expected first node ready")
	}
	if first.InternalIP != "10.0.0.20" {
		t.Errorf("expected internal ip, got %q", first.InternalIP)
	}
	if first.OS != "linux" || first.Architecture != "amd64" {
		t.Errorf("unexpected os/arch: %s/%s", first.OS, first.Architecture)
	}
	if len(first.Roles) != 1 || first.Roles[0] != "worker" {
		t.Errorf("expected worker role, got %v", first.Roles)
	}
	if nodes[1].Ready {
		t.Error("expected second node not ready")
	}
}

func TestClientListPods(t *testing.T) {
	stub := newStubKubernetesAPI()
	defer stub.close()
	stub.podList = `{
	  "apiVersion": "v1",
	  "kind": "PodList",
	  "items": [
	    {"metadata": {"name": "web-1", "namespace": "default"},
	     "spec": {"nodeName": "edge-worker-01"},
	     "status": {"phase": "Running", "containerStatuses": [{"restartCount": 2}]}},
	    {"metadata": {"name": "crash-1", "namespace": "default"},
	     "status": {"phase": "Failed", "containerStatuses": [{"restartCount": 5}]}}
	  ]
	}`

	client, err := newTestClient(stub)
	if err != nil {
		t.Fatalf("building client: %v", err)
	}

	pods, err := client.ListPods(context.Background(), "")
	if err != nil {
		t.Fatalf("ListPods: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("expected 2 pods, got %d", len(pods))
	}
	if pods[0].Name != "web-1" || pods[0].Phase != "Running" || pods[0].RestartCount != 2 {
		t.Errorf("unexpected first pod: %+v", pods[0])
	}
	if pods[1].Phase != "Failed" {
		t.Errorf("expected failed pod, got %+v", pods[1])
	}
}

func TestClientCreateDeleteNamespace(t *testing.T) {
	stub := newStubKubernetesAPI()
	defer stub.close()

	client, err := newTestClient(stub)
	if err != nil {
		t.Fatalf("building client: %v", err)
	}

	if err := client.CreateNamespace(context.Background(), "aether-grid-test"); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}
	if len(stub.createdNS) != 1 {
		t.Fatalf("expected namespace created, got %v", stub.createdNS)
	}
	if err := client.DeleteNamespace(context.Background(), "aether-grid-test"); err != nil {
		t.Fatalf("DeleteNamespace: %v", err)
	}
	if len(stub.deletedNS) != 1 || stub.deletedNS[0] != "aether-grid-test" {
		t.Fatalf("unexpected deletions: %v", stub.deletedNS)
	}
}

func TestClientRequestTimeout(t *testing.T) {
	stub := newStubKubernetesAPI()
	defer stub.close()
	stub.sleep = 500 * time.Millisecond

	client, err := newTestClient(stub)
	if err != nil {
		t.Fatalf("building client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = client.GetClusterInfo(ctx)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !IsCode(err, CodeTimeout) {
		t.Fatalf("expected CodeTimeout, got %v", err)
	}
}

func TestClientUnavailable(t *testing.T) {
	// Point the client at a closed server: the API is unreachable.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()

	client, err := NewForTestConfig(server.URL)
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	_, err = client.GetClusterInfo(context.Background())
	if err == nil {
		t.Fatal("expected an error for an unreachable API")
	}
	if !IsCode(err, CodeUnavailable) {
		t.Fatalf("expected CodeUnavailable, got %v", err)
	}
}

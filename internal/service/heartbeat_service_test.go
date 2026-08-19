package service

import (
	"context"
	"testing"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
)

func TestHeartbeatServiceRecord(t *testing.T) {
	repo := newMockNodeRepository()
	heartbeatSvc := NewHeartbeatService(repo)
	nodeSvc := NewNodeService(repo)

	created, err := nodeSvc.Create(context.Background(), CreateNodeInput{Name: "edge-01"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	node, err := heartbeatSvc.Record(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("record heartbeat failed: %v", err)
	}

	if node.LastHeartbeat == nil {
		t.Fatal("expected last_heartbeat to be set")
	}
	elapsed := time.Since(*node.LastHeartbeat)
	if elapsed > time.Second {
		t.Errorf("last_heartbeat too old: %v", elapsed)
	}
	if node.DesiredStatus != domain.DesiredInitialStatus {
		t.Errorf("heartbeat must not change desired status; got %q", node.DesiredStatus)
	}
	if node.Status != domain.InitialStatus {
		t.Errorf("heartbeat must not change actual status; got %q", node.Status)
	}
}

func TestHeartbeatServiceUpdatesTimestamp(t *testing.T) {
	repo := newMockNodeRepository()
	nodeSvc := NewNodeService(repo)
	heartbeatSvc := NewHeartbeatService(repo)

	created, err := nodeSvc.Create(context.Background(), CreateNodeInput{Name: "edge-01"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	first, err := heartbeatSvc.Record(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("first heartbeat failed: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := heartbeatSvc.Record(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("second heartbeat failed: %v", err)
	}

	if first.LastHeartbeat == nil || second.LastHeartbeat == nil {
		t.Fatal("expected heartbeats to be recorded")
	}
	if !second.LastHeartbeat.After(*first.LastHeartbeat) {
		t.Error("expected second heartbeat timestamp to be later")
	}
}

func TestHeartbeatServiceRepeatedHeartbeatsNoDuplicates(t *testing.T) {
	repo := newMockNodeRepository()
	nodeSvc := NewNodeService(repo)
	heartbeatSvc := NewHeartbeatService(repo)

	created, err := nodeSvc.Create(context.Background(), CreateNodeInput{Name: "edge-01"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := heartbeatSvc.Record(context.Background(), created.ID); err != nil {
			t.Fatalf("heartbeat %d failed: %v", i, err)
		}
	}

	nodes, err := nodeSvc.List(context.Background())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("expected exactly 1 node after repeated heartbeats, got %d", len(nodes))
	}
}

func TestHeartbeatServiceNotFound(t *testing.T) {
	svc := NewHeartbeatService(newMockNodeRepository())

	if _, err := svc.Record(context.Background(), "missing"); !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

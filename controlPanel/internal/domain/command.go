package domain

import (
	"encoding/json"
	"time"
)

// CommandStatus is a strongly typed representation of a command's lifecycle.
type CommandStatus string

// Command lifecycle statuses.
const (
	CommandPending   CommandStatus = "PENDING"
	CommandExecuting CommandStatus = "EXECUTING"
	CommandSucceeded CommandStatus = "SUCCEEDED"
	CommandFailed    CommandStatus = "FAILED"
)

// allCommandStatuses is the canonical set of valid command statuses.
var allCommandStatuses = []CommandStatus{
	CommandPending,
	CommandExecuting,
	CommandSucceeded,
	CommandFailed,
}

// Valid reports whether s is a known command status.
func (s CommandStatus) Valid() bool {
	for _, candidate := range allCommandStatuses {
		if s == candidate {
			return true
		}
	}
	return false
}

// Terminal reports whether the command has reached a final state. Terminal
// commands are never overwritten by duplicate result reports.
func (s CommandStatus) Terminal() bool {
	return s == CommandSucceeded || s == CommandFailed
}

// Command is the domain model for an instruction the control plane sends to
// an edge node agent. It is independent of both HTTP and persistence concerns.
type Command struct {
	ID         string
	NodeID     string
	Type       string
	Parameters json.RawMessage
	Status     CommandStatus
	Result     json.RawMessage
	Error      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

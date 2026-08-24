package terraform

import (
	"encoding/json"
	"fmt"
	"strings"
)

// changeAction is one action in a terraform change set.
const (
	actionCreate  = "create"
	actionDelete  = "delete"
	actionUpdate  = "update"
	actionNoOp    = "no-op"
	actionDestroy = "destroy"
)

// planShow is the JSON shape produced by `terraform show -json plan.tfplan`.
type planShow struct {
	ResourceChanges []resourceChange `json:"resource_changes"`
}

type resourceChange struct {
	Address string `json:"address"`
	Change  struct {
		Actions []string `json:"actions"`
	} `json:"change"`
}

// parsePlanChanges counts create/update/delete actions from a plan file's
// JSON representation.
func parsePlanChanges(showJSON []byte) (toCreate, toModify, toDestroy int, err error) {
	var show planShow
	if err := json.Unmarshal(showJSON, &show); err != nil {
		return 0, 0, 0, fmt.Errorf("decoding plan output: %w", err)
	}

	for _, change := range show.ResourceChanges {
		for _, action := range change.Change.Actions {
			switch action {
			case actionCreate:
				toCreate++
			case actionDelete, actionDestroy:
				toDestroy++
			case actionUpdate:
				toModify++
			}
		}
	}
	return toCreate, toModify, toDestroy, nil
}

// outputValue is one entry of `terraform output -json`.
type outputValue struct {
	Value json.RawMessage `json:"value"`
}

// outputShow is the JSON shape produced by `terraform output -json`.
type outputShow struct {
	NodeCount   outputValue `json:"node_count"`
	NodeIDs     outputValue `json:"node_ids"`
	NodeNames   outputValue `json:"node_names"`
	NodeAddress outputValue `json:"node_addresses"`
}

// parseOutputNodes extracts provisioned nodes from `terraform output -json`.
func parseOutputNodes(outputJSON []byte) (nodeIDs, nodeNames, nodeAddresses []string, err error) {
	var show outputShow
	if err := json.Unmarshal(outputJSON, &show); err != nil {
		return nil, nil, nil, fmt.Errorf("decoding output: %w", err)
	}

	decode := func(raw json.RawMessage) ([]string, error) {
		if len(raw) == 0 || string(raw) == "null" {
			return nil, nil
		}
		var values []string
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("decoding output list: %w", err)
		}
		return values, nil
	}

	nodeIDs, err = decode(show.NodeIDs.Value)
	if err != nil {
		return nil, nil, nil, err
	}
	nodeNames, err = decode(show.NodeNames.Value)
	if err != nil {
		return nil, nil, nil, err
	}
	nodeAddresses, err = decode(show.NodeAddress.Value)
	if err != nil {
		return nil, nil, nil, err
	}
	return nodeIDs, nodeNames, nodeAddresses, nil
}

// truncate caps the length of diagnostic output so error messages never carry
// unbounded provider output. Secrets and credentials are never written into
// generated configuration, so this is purely defensive.
func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("... (%d bytes omitted)", len(s)-max)
}

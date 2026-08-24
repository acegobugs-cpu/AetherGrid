package terraform

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Root module configuration templates. These are deliberately small: the real
// resources live in the edge-node module under terraform/modules/edge-node,
// keeping Terraform code in HCL files rather than Go string literals (§23 of
// the Phase 6 spec). The root module only wires variables through to the
// module, and the provider is the bundled builtin "local" provider so no
// plugin download or cloud credentials are required (§52-56).

const rootMainTf = `terraform {
  required_providers {
    local = {
      source = "terraform.io/builtin/local"
    }
  }
}

module "edge_node" {
  source     = "%s"

  node_count = var.node_count
  cpu        = var.cpu
  memory_mb  = var.memory_mb
  disk_gb    = var.disk_gb
  image      = var.image
  base_name  = var.base_name
  output_dir = var.output_dir
}
`

const rootVariablesTf = `variable "node_count" {
  type = number
}

variable "cpu" {
  type = number
}

variable "memory_mb" {
  type = number
}

variable "disk_gb" {
  type = number
}

variable "image" {
  type = string
}

variable "base_name" {
  type = string
}

variable "output_dir" {
  type = string
}
`

const rootTfVars = `node_count = %d
cpu        = %d
memory_mb  = %d
disk_gb    = %d
image      = %q
base_name  = %q
output_dir = %q
`

// sanitizeBaseName reduces an infrastructure name to the safe charset
// [a-z0-9-] used in resource identifiers. This prevents both HCL injection
// and path traversal from flowing into generated configuration.
func sanitizeBaseName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "infra"
	}
	if len(result) > 63 {
		return result[:63]
	}
	return result
}

// writeConfig renders and writes the root module configuration (main.tf,
// variables.tf and terraform.tfvars) for one infrastructure deployment.
// moduleSource is the path to the edge-node Terraform module; it is taken
// from the Provisioner so the module is located wherever it was configured.
func writeConfig(dir, moduleSource, infraName string, nodeCount, cpu, memoryMB, diskGB int, image string) error {
	baseName := sanitizeBaseName(infraName)
	outputDir := filepath.Join(dir, "nodes")

	main := fmt.Sprintf(rootMainTf, moduleSource)
	variables := rootVariablesTf
	tfvars := fmt.Sprintf(rootTfVars,
		nodeCount, cpu, memoryMB, diskGB,
		strconv.Quote(image),
		strconv.Quote(baseName),
		strconv.Quote(outputDir),
	)

	files := map[string]string{
		"main.tf":          main,
		"variables.tf":     variables,
		"terraform.tfvars": tfvars,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return nil
}

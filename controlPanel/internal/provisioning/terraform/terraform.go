package terraform

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/provisioning"
)

// Provisioner is the Terraform-backed implementation of provisioning.Provisioner.
// Every infrastructure deployment gets an isolated working directory under the
// configured work dir; the provisioner never shares or caches state between
// deployments.
type Provisioner struct {
	bin       string
	workDir   string
	moduleDir string
	timeout   time.Duration
	logger    *log.Logger
	runner    runner
}

// NewProvisioner constructs a Terraform provisioner. bin is the Terraform
// binary, workDir the base directory for per-deployment working directories,
// moduleDir the absolute path of the edge-node module, and timeout bounds each
// external command.
func NewProvisioner(bin, workDir, moduleDir string, timeout time.Duration, logger *log.Logger) *Provisioner {
	return &Provisioner{
		bin:       bin,
		workDir:   workDir,
		moduleDir: moduleDir,
		timeout:   timeout,
		logger:    logger,
		runner:    osRunner{},
	}
}

// workdirFor returns the isolated working directory for one deployment.
func (p *Provisioner) workdirFor(infra *domain.Infrastructure) string {
	return filepath.Join(p.workDir, infra.ID)
}

// prepare creates the working directory and renders the root module config.
func (p *Provisioner) prepare(dir string, infra *domain.Infrastructure) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating workdir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nodes"), 0o700); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}
	if err := writeConfig(dir, p.moduleDir, infra.Spec.Name, infra.Spec.NodeCount, infra.Spec.CPU,
		infra.Spec.MemoryMB, infra.Spec.DiskGB, infra.Spec.Image); err != nil {
		return err
	}
	return nil
}

// init runs `terraform init` once in a working directory.
func (p *Provisioner) init(ctx context.Context, dir string) error {
	result, err := p.run(ctx, dir, "init", "-input=false", "-no-color", "-backend=false")
	if err != nil {
		if result.exitCode == 0 {
			// Unexpected: the runner reported an error but terraform exited 0.
			return nil
		}
		return p.mapError(provisioning.KindTerraformInitFailed, err, result)
	}
	return nil
}

// run executes a single terraform command within the timeout and cancellation
// policy.
func (p *Provisioner) run(ctx context.Context, dir string, args ...string) (commandResult, error) {
	commandArgs := append([]string{args[0]}, args[1:]...)
	cmdCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.runner.run(cmdCtx, dir, p.bin, commandArgs...)
}

// mapError converts a failed command into a structured provisioning error.
func (p *Provisioner) mapError(kind provisioning.ErrorKind, cause error, result commandResult) error {
	detail := strings.TrimSpace(result.stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.stdout)
	}
	if detail == "" {
		detail = cause.Error()
	}
	return &provisioning.Error{Kind: kind, Message: truncate(detail, 1000), Cause: cause}
}

// Plan implements provisioning.Provisioner.
func (p *Provisioner) Plan(ctx context.Context, infra *domain.Infrastructure) (*provisioning.PlanResult, error) {
	if err := infra.Spec.Validate(); err != nil {
		return nil, &provisioning.Error{Kind: provisioning.KindInvalidSpecification, Message: err.Error()}
	}

	dir := p.workdirFor(infra)
	if err := p.prepare(dir, infra); err != nil {
		return nil, &provisioning.Error{Kind: provisioning.KindTerraformPlanFailed, Message: err.Error()}
	}
	if err := p.init(ctx, dir); err != nil {
		return nil, err
	}

	planResult, err := p.run(ctx, dir, "plan", "-input=false", "-no-color", "-out=plan.tfplan")
	if err != nil && planResult.exitCode != 2 {
		return nil, p.mapError(provisioning.KindTerraformPlanFailed, err, planResult)
	}

	showResult, err := p.run(ctx, dir, "show", "-json", "plan.tfplan")
	if err != nil {
		return nil, p.mapError(provisioning.KindTerraformPlanFailed, err, showResult)
	}

	toCreate, toModify, toDestroy, err := parsePlanChanges([]byte(showResult.stdout))
	if err != nil {
		return nil, &provisioning.Error{
			Kind:    provisioning.KindOutputUnavailable,
			Message: truncate(showResult.stdout, 500),
			Cause:   err,
		}
	}

	return &provisioning.PlanResult{
		Changes: domain.ChangeSummary{ToCreate: toCreate, ToModify: toModify, ToDestroy: toDestroy},
		Output:  truncate(showResult.stdout, 2000),
	}, nil
}

// Apply implements provisioning.Provisioner.
func (p *Provisioner) Apply(ctx context.Context, infra *domain.Infrastructure) (*provisioning.ApplyResult, error) {
	if err := infra.Spec.Validate(); err != nil {
		return nil, &provisioning.Error{Kind: provisioning.KindInvalidSpecification, Message: err.Error()}
	}

	dir := p.workdirFor(infra)
	if err := p.prepare(dir, infra); err != nil {
		return nil, &provisioning.Error{Kind: provisioning.KindTerraformApplyFailed, Message: err.Error()}
	}
	if err := p.init(ctx, dir); err != nil {
		return nil, err
	}

	planResult, err := p.run(ctx, dir, "plan", "-input=false", "-no-color", "-out=plan.tfplan")
	if err != nil && planResult.exitCode != 2 {
		return nil, p.mapError(provisioning.KindTerraformPlanFailed, err, planResult)
	}

	showResult, err := p.run(ctx, dir, "show", "-json", "plan.tfplan")
	if err != nil {
		return nil, p.mapError(provisioning.KindTerraformPlanFailed, err, showResult)
	}
	toCreate, toModify, toDestroy, err := parsePlanChanges([]byte(showResult.stdout))
	if err != nil {
		return nil, &provisioning.Error{
			Kind:    provisioning.KindOutputUnavailable,
			Message: truncate(showResult.stdout, 500),
			Cause:   err,
		}
	}

	applyResult, err := p.run(ctx, dir, "apply", "-input=false", "-no-color", "-auto-approve", "plan.tfplan")
	if err != nil {
		return nil, p.mapError(provisioning.KindTerraformApplyFailed, err, applyResult)
	}

	outputResult, err := p.run(ctx, dir, "output", "-json")
	if err != nil {
		return nil, p.mapError(provisioning.KindOutputUnavailable, err, outputResult)
	}

	nodeIDs, nodeNames, nodeAddresses, err := parseOutputNodes([]byte(outputResult.stdout))
	if err != nil {
		return nil, &provisioning.Error{
			Kind:    provisioning.KindOutputUnavailable,
			Message: truncate(outputResult.stdout, 500),
			Cause:   err,
		}
	}

	nodes := make([]domain.InfrastructureNode, 0, len(nodeIDs))
	for i := range nodeIDs {
		name := ""
		if i < len(nodeNames) {
			name = nodeNames[i]
		}
		address := ""
		if i < len(nodeAddresses) {
			address = nodeAddresses[i]
		}
		nodes = append(nodes, domain.InfrastructureNode{
			ID:    nodeIDs[i],
			Name:  name,
			IP:    address,
			State: "running",
		})
	}

	return &provisioning.ApplyResult{
		Changes: domain.ChangeSummary{ToCreate: toCreate, ToModify: toModify, ToDestroy: toDestroy},
		Nodes:   nodes,
	}, nil
}

// Destroy implements provisioning.Provisioner.
func (p *Provisioner) Destroy(ctx context.Context, infra *domain.Infrastructure) error {
	dir := p.workdirFor(infra)

	// Nothing was ever planned/applied for this deployment.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	if err := p.init(ctx, dir); err != nil {
		return err
	}

	result, err := p.run(ctx, dir, "destroy", "-input=false", "-no-color", "-auto-approve")
	if err != nil {
		return p.mapError(provisioning.KindTerraformDestroyFailed, err, result)
	}
	return nil
}

// Status implements provisioning.Provisioner.
func (p *Provisioner) Status(ctx context.Context, infra *domain.Infrastructure) (*domain.InfrastructureStatus, error) {
	dir := p.workdirFor(infra)

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return &domain.InfrastructureStatus{Phase: domain.InfraPhasePending}, nil
	}

	// If terraform output is unavailable the deployment has no applied state.
	outputResult, err := p.run(ctx, dir, "output", "-json")
	if err != nil {
		return &domain.InfrastructureStatus{Phase: domain.InfraPhasePending}, nil
	}

	nodeIDs, nodeNames, nodeAddresses, err := parseOutputNodes([]byte(outputResult.stdout))
	if err != nil {
		return nil, &provisioning.Error{
			Kind:    provisioning.KindOutputUnavailable,
			Message: truncate(outputResult.stdout, 500),
			Cause:   err,
		}
	}

	nodes := make([]domain.InfrastructureNode, 0, len(nodeIDs))
	for i := range nodeIDs {
		name := ""
		if i < len(nodeNames) {
			name = nodeNames[i]
		}
		address := ""
		if i < len(nodeAddresses) {
			address = nodeAddresses[i]
		}
		nodes = append(nodes, domain.InfrastructureNode{
			ID:    nodeIDs[i],
			Name:  name,
			IP:    address,
			State: "running",
		})
	}

	if len(nodes) == 0 {
		return &domain.InfrastructureStatus{Phase: domain.InfraPhasePending}, nil
	}
	return &domain.InfrastructureStatus{Phase: domain.InfraPhaseReady, Nodes: nodes}, nil
}
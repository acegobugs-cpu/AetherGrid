# AETHER-GRID Operator — developer tooling.
#
# The two controller-gen invocations are the canonical code-generation steps.
# Generated artifacts (deepcopy code and CRD manifests) are committed and must
# not be edited by hand; re-run these targets after changing the API types.

CONTROLLER_GEN ?= bin/controller-gen

.PHONY: controller-gen
controller-gen:
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5

.PHONY: generate
generate: ## Regenerate deepcopy code and CRD manifests.
	$(CONTROLLER_GEN) object paths=./api/...
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:artifacts:config=config/crd/bases
	$(CONTROLLER_GEN) rbac:roleName=aether-grid-operator paths=./internal/... output:rbac:artifacts:config=config/rbac

.PHONY: fmt
fmt:
	gofmt -w .

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test ./... -count=1

.PHONY: test-race
test-race:
	go test -race ./... -count=1

.PHONY: build
build:
	go build ./...

.PHONY: docker-build
docker-build: ## Build the operator container image.
	docker build -t aether-grid-operator:latest .

.PHONY: manifests
manifests: ## Render the full install manifest set via kustomize (kubectl apply -k works too).
	kubectl kustomize config/default > dist/install.yaml

.PHONY: run
run: ## Run the operator locally against the current kubeconfig.
	go run ./cmd/operator --leader-elect=false --health-probe-bind-address=:8081 --metrics-bind-address=:8080
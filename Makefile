.PHONY: build run test lint clean docker helm-lint helm-template helm-install helm-uninstall helm-upgrade

GO ?= go
IMAGE ?= cilium-egress-operator
TAG ?= latest
NAMESPACE ?= kube-system
HELM ?= helm
CHART_DIR ?= deploy/charts/cilium-egress-operator
RELEASE_NAME ?= cilium-egress-operator

build:
	$(GO) build -o bin/manager .

run:
	$(GO) run ./main.go --leader-elect

test:
	$(GO) test ./... -v -race

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

docker:
	docker build -t $(IMAGE):$(TAG) .

generate:
	$(GO) generate ./...

manifests:
	controller-gen crd paths=./api/... output:crd:dir=config/crd
	cp config/crd/egressgateway.yaml $(CHART_DIR)/crds/

fmt:
	gofmt -w .
	goimports -w .

vet:
	$(GO) vet ./...

vendor:
	$(GO) mod vendor

helm-lint:
	$(HELM) lint $(CHART_DIR)

helm-template:
	$(HELM) template $(RELEASE_NAME) $(CHART_DIR) --namespace $(NAMESPACE)

helm-install: helm-lint
	$(HELM) install $(RELEASE_NAME) $(CHART_DIR) --namespace $(NAMESPACE) --create-namespace

helm-upgrade: helm-lint
	$(HELM) upgrade $(RELEASE_NAME) $(CHART_DIR) --namespace $(NAMESPACE)

helm-uninstall:
	$(HELM) uninstall $(RELEASE_NAME) --namespace $(NAMESPACE)

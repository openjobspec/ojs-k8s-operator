VERSION ?= 0.5.0
IMG ?= ghcr.io/openjobspec/ojs-k8s-operator:v$(VERSION)
HELM_CHART_DIR = charts/ojs-operator

.PHONY: build test lint run docker-build docker-push install uninstall deploy undeploy generate helm-lint helm-template helm-package helm-install helm-uninstall

##@ Build

build:
	go build -o bin/manager ./cmd/manager

test:
	go test ./... -race -cover

lint:
	go vet ./...

run: build
	./bin/manager

##@ Docker

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(IMG) .

docker-push:
	docker push $(IMG)

##@ Deployment

install:
	kubectl apply -f config/crd/ojscluster-crd.yaml
	kubectl apply -f config/crd/ojsworker-crd.yaml

uninstall:
	kubectl delete -f config/crd/ojsworker-crd.yaml
	kubectl delete -f config/crd/ojscluster-crd.yaml

deploy: install
	kubectl apply -f config/rbac/service_account.yaml
	kubectl apply -f config/rbac/role.yaml
	kubectl apply -f config/rbac/role_binding.yaml
	kubectl apply -f config/manager/deployment.yaml

undeploy:
	kubectl delete -f config/manager/deployment.yaml
	kubectl delete -f config/rbac/role_binding.yaml
	kubectl delete -f config/rbac/role.yaml
	kubectl delete -f config/rbac/service_account.yaml
	kubectl delete -f config/crd/ojsworker-crd.yaml
	kubectl delete -f config/crd/ojscluster-crd.yaml

##@ Helm

helm-lint:
	helm lint $(HELM_CHART_DIR)

helm-template:
	helm template test $(HELM_CHART_DIR) --values $(HELM_CHART_DIR)/values.yaml

helm-package:
	helm package $(HELM_CHART_DIR)

helm-install:
	helm install ojs-operator $(HELM_CHART_DIR) --create-namespace --namespace ojs-system

helm-uninstall:
	helm uninstall ojs-operator --namespace ojs-system

##@ Code Generation

generate:
	go generate ./...

IMG ?= ghcr.io/openjobspec/ojs-k8s-operator:latest

.PHONY: build test lint run docker-build docker-push install uninstall deploy undeploy

build:
	go build -o bin/manager ./cmd/manager

test:
	go test ./... -race -cover

lint:
	go vet ./...

run: build
	./bin/manager

docker-build:
	docker build -t $(IMG) .

docker-push:
	docker push $(IMG)

install:
	kubectl apply -f config/crd/ojscluster-crd.yaml

uninstall:
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
	kubectl delete -f config/crd/ojscluster-crd.yaml

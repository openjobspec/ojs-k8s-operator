# OJS Kubernetes Operator

A Kubernetes operator for managing [Open Job Spec](https://github.com/openjobspec/openjobspec) clusters using custom resources.

## Overview

The OJS Kubernetes Operator automates the deployment and management of OJS server clusters on Kubernetes. It introduces the `OJSCluster` custom resource that declaratively defines an OJS deployment including backend configuration, scaling, and monitoring.

## Features

- **Declarative cluster management** — Define OJS clusters as Kubernetes custom resources
- **Multiple backend support** — Redis, PostgreSQL, or NATS backends
- **Embedded backends** — Auto-deploy Redis alongside the OJS server
- **Auto-scaling** — Scale based on queue depth and active jobs per worker
- **Monitoring** — Prometheus ServiceMonitor and Grafana dashboard support
- **Production-ready** — Health checks, graceful shutdown, leader election

## Quick Start

### Prerequisites

- Kubernetes cluster (v1.26+)
- `kubectl` configured

### Install the CRD

```bash
make install
```

### Deploy the operator

```bash
make deploy
```

### Create an OJS cluster

```bash
kubectl apply -f config/samples/basic.yaml
```

### Check status

```bash
kubectl get ojsclusters
```

## Custom Resource: OJSCluster

### Minimal example

```yaml
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSCluster
metadata:
  name: my-ojs
spec:
  backend:
    type: redis
    url: "redis://redis:6379"
  replicas: 2
```

### Production example with embedded Redis and auto-scaling

```yaml
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSCluster
metadata:
  name: ojs-production
spec:
  backend:
    type: redis
    embedded: true
  replicas: 3
  image: ghcr.io/openjobspec/ojs-backend-redis:latest
  autoScaling:
    enabled: true
    minReplicas: 3
    maxReplicas: 10
    targetQueueDepth: 100
  resources:
    requests:
      cpu: "250m"
      memory: "256Mi"
    limits:
      cpu: "1"
      memory: "512Mi"
  monitoring:
    enabled: true
    serviceMonitor: true
```

### Using a Secret for backend URL

```yaml
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSCluster
metadata:
  name: ojs-secret
spec:
  backend:
    type: redis
    urlSecretRef:
      name: redis-credentials
      key: url
```

## Spec Reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `backend.type` | string | *required* | Backend type: `redis`, `postgres`, or `nats` |
| `backend.url` | string | | Backend connection URL |
| `backend.urlSecretRef` | object | | Secret reference for connection URL |
| `backend.embedded` | bool | `false` | Auto-deploy the backend |
| `replicas` | int32 | `2` | Number of OJS server replicas |
| `image` | string | `ghcr.io/openjobspec/ojs-server:latest` | OJS server container image |
| `autoScaling.enabled` | bool | | Enable HPA-based auto-scaling |
| `autoScaling.minReplicas` | int32 | | Minimum replica count |
| `autoScaling.maxReplicas` | int32 | | Maximum replica count |
| `autoScaling.targetQueueDepth` | int64 | | Target queue depth per replica |
| `resources` | object | | Pod resource requests/limits |
| `monitoring.enabled` | bool | | Enable Prometheus metrics |
| `monitoring.serviceMonitor` | bool | | Create ServiceMonitor resource |
| `monitoring.grafanaDashboard` | bool | | Create Grafana dashboard ConfigMap |

## Status

The operator updates `.status` with:

| Field | Description |
|-------|-------------|
| `phase` | Cluster phase: `Pending`, `Running`, `Scaling`, `Error` |
| `replicas` | Total server pod count |
| `readyReplicas` | Ready server pod count |
| `queueDepth` | Current queued jobs |
| `activeJobs` | Current active jobs |
| `conditions` | Standard Kubernetes conditions |

## Development

```bash
# Build
make build

# Run tests
make test

# Lint
make lint

# Run locally (requires kubeconfig)
make run

# Build Docker image
make docker-build
```

## Architecture

The operator follows the standard controller-runtime pattern:

1. **CRD** (`OJSCluster`) defines the desired state
2. **Controller** watches for changes and reconciles:
   - Creates/updates a **Deployment** for the OJS server
   - Creates/updates a **Service** for HTTP access
   - Creates/updates a **ConfigMap** with backend configuration
   - Optionally creates an embedded **Redis** deployment
   - Updates **status** with current cluster state

## License

Apache License 2.0 — see [LICENSE](../LICENSE) for details.

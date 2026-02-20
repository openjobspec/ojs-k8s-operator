# OJS Kubernetes Operator

> [!NOTE]
> **🔧 Beta** — This project is under active development. APIs are stabilizing but may still change. Feedback and contributions are welcome.

A Kubernetes operator for managing [Open Job Spec](https://github.com/openjobspec/openjobspec) clusters and workers using custom resources.

## Overview

The OJS Kubernetes Operator automates the deployment and lifecycle management of OJS server clusters on Kubernetes. It provides two Custom Resource Definitions:

- **OJSCluster** — Manages OJS server deployments with backend configuration, auto-scaling, and monitoring
- **OJSWorker** — Manages worker deployments that process jobs from an OJSCluster

## Features

- **Declarative cluster management** — Define OJS clusters as Kubernetes custom resources
- **All 6 backend types** — Redis, PostgreSQL, NATS, Kafka, SQS, and Lite backends
- **Embedded backends** — Auto-deploy Redis alongside the OJS server
- **Worker management** — Deploy and scale workers per job type and queue
- **HPA-based auto-scaling** — Automatic HorizontalPodAutoscaler creation when worker autoscaling is enabled
- **Queue-based auto-scaling** — Scale workers based on queue depth metrics
- **Webhook validation** — Validating admission webhooks for OJSCluster and OJSWorker CRDs
- **Monitoring** — Prometheus ServiceMonitor and Grafana dashboard support
- **Production-ready** — Health checks, graceful shutdown, leader election, RBAC

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                     │
│                                                          │
│  ┌──────────────────┐                                    │
│  │  OJS Operator     │                                   │
│  │  (controller-mgr) │                                   │
│  └────────┬─────────┘                                    │
│           │ watches & reconciles                         │
│           ▼                                              │
│  ┌────────────────┐     ┌─────────────────┐              │
│  │  OJSCluster CR  │────▶│ OJS Server Pods │              │
│  │  (desired state)│     │ (Deployment)    │              │
│  └────────────────┘     │ + Service       │              │
│                          │ + ConfigMap     │              │
│                          └────────┬────────┘              │
│                                   │                      │
│  ┌────────────────┐     ┌────────▼────────┐              │
│  │  OJSWorker CR   │────▶│ Worker Pods     │              │
│  │  (desired state)│     │ (Deployment)    │              │
│  └────────────────┘     └─────────────────┘              │
│                                                          │
│  ┌──────────────────────────────────────┐                │
│  │  Backend (Redis / PostgreSQL / NATS / Kafka / SQS / Lite)  │                │
│  │  (external or embedded)               │                │
│  └──────────────────────────────────────┘                │
└─────────────────────────────────────────────────────────┘
```

The operator follows the standard controller-runtime pattern:

1. **OJSClusterReconciler** watches `OJSCluster` CRs and reconciles:
   - A **Deployment** for the OJS server pods
   - A **Service** for HTTP and metrics access
   - A **ConfigMap** with backend configuration
   - Optionally an embedded **Redis** deployment and service
   - **Status** updates with phase, replica counts, and conditions

2. **OJSWorkerReconciler** watches `OJSWorker` CRs and reconciles:
   - A **Deployment** for worker pods connected to the referenced OJSCluster
   - **Status** updates with phase, replica counts, and queue depth
   - Periodic re-queue for auto-scaling when enabled

## Prerequisites

- Kubernetes cluster (v1.26+)
- `kubectl` configured to access the cluster
- `helm` v3 (for Helm-based installation)
- cert-manager (optional, for webhook certificates)

## Installation

### Option 1: kubectl apply

```bash
# Install CRDs
make install

# Deploy the operator
make deploy
```

### Option 2: Helm

```bash
# Install from local chart
helm install ojs-operator charts/ojs-operator \
  --create-namespace \
  --namespace ojs-system

# Or with custom values
helm install ojs-operator charts/ojs-operator \
  --namespace ojs-system \
  --set image.tag=v0.2.0 \
  --set replicaCount=2
```

### Option 3: OLM (Operator Lifecycle Manager)

```bash
# If your cluster has OLM installed
kubectl apply -f https://raw.githubusercontent.com/openjobspec/ojs-k8s-operator/main/config/olm/catalogsource.yaml
kubectl apply -f https://raw.githubusercontent.com/openjobspec/ojs-k8s-operator/main/config/olm/subscription.yaml
```

## CRD Reference

### OJSCluster

Defines an OJS server cluster deployment.

#### Spec Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `backend.type` | string | *required* | Backend type: `redis`, `postgres`, `nats`, `kafka`, `sqs`, or `lite` |
| `backend.url` | string | | Backend connection URL |
| `backend.urlSecretRef.name` | string | | Secret name containing the connection URL |
| `backend.urlSecretRef.key` | string | | Key within the Secret |
| `backend.embedded` | bool | `false` | Auto-deploy the backend (Redis only) |
| `replicas` | int32 | `2` | Number of OJS server replicas |
| `image` | string | `ghcr.io/openjobspec/ojs-server:latest` | OJS server container image |
| `resources` | ResourceRequirements | | Pod resource requests/limits |
| `autoScaling.enabled` | bool | | Enable auto-scaling |
| `autoScaling.minReplicas` | int32 | | Minimum replica count |
| `autoScaling.maxReplicas` | int32 | | Maximum replica count |
| `autoScaling.targetQueueDepth` | int64 | | Target queue depth per replica |
| `autoScaling.targetJobsPerWorker` | int64 | | Target active jobs per worker |
| `autoScaling.scaleUpCooldown` | string | | Cooldown after scale-up (e.g., `60s`) |
| `autoScaling.scaleDownCooldown` | string | | Cooldown after scale-down (e.g., `300s`) |
| `monitoring.enabled` | bool | | Enable Prometheus metrics |
| `monitoring.serviceMonitor` | bool | | Create ServiceMonitor resource |
| `monitoring.grafanaDashboard` | bool | | Create Grafana dashboard ConfigMap |

#### Status Fields

| Field | Description |
|-------|-------------|
| `phase` | Cluster phase: `Pending`, `Running`, `Scaling`, `Error` |
| `replicas` | Total server pod count |
| `readyReplicas` | Ready server pod count |
| `queueDepth` | Current queued jobs |
| `activeJobs` | Current active jobs |
| `conditions` | Standard Kubernetes conditions (`Ready`, `BackendReady`) |

### OJSWorker

Defines a worker deployment that processes jobs from an OJSCluster.

#### Spec Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `clusterRef` | string | *required* | Name of the OJSCluster to connect to |
| `jobTypes` | []string | *required* | Job types this worker handles |
| `queues` | []string | `["default"]` | Queues to process |
| `concurrency` | int32 | | Concurrent jobs per pod |
| `replicas` | int32 | `1` | Number of worker pods |
| `image` | string | *required* | Worker container image |
| `command` | []string | | Command override |
| `env` | []EnvVar | | Additional environment variables |
| `resources` | ResourceRequirements | | Pod resource requests/limits |
| `autoScaling.enabled` | bool | | Enable queue-based auto-scaling |
| `autoScaling.minReplicas` | int32 | | Minimum worker replicas |
| `autoScaling.maxReplicas` | int32 | | Maximum worker replicas |
| `autoScaling.targetJobsPerWorker` | int64 | | Desired pending jobs per worker |
| `autoScaling.scaleUpThreshold` | int64 | | Queue depth to trigger scale-up |
| `autoScaling.scaleDownDelay` | string | | Delay before scale-down (e.g., `5m`) |
| `autoScaling.pollingInterval` | string | | Queue metrics polling interval (e.g., `30s`) |
| `gracefulShutdown.timeoutSeconds` | int32 | `30` | Max time to wait for active jobs |
| `gracefulShutdown.drainBeforeShutdown` | bool | | Wait for active jobs to complete |

#### Status Fields

| Field | Description |
|-------|-------------|
| `phase` | Worker phase: `Pending`, `Running`, `Scaling`, `Draining`, `Error` |
| `replicas` | Total worker pod count |
| `readyReplicas` | Ready worker pod count |
| `activeJobs` | Jobs being processed |
| `queueDepth` | Pending jobs for this worker's queues |
| `lastScaleTime` | Last scaling event timestamp |
| `conditions` | Standard Kubernetes conditions |

## Examples

### Minimal Cluster

The simplest OJSCluster with an embedded Redis backend:

```yaml
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSCluster
metadata:
  name: ojs-minimal
spec:
  backend:
    type: redis
    embedded: true
  replicas: 1
```

### Production Setup

A production-ready cluster with external Redis, resource limits, and monitoring:

```yaml
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSCluster
metadata:
  name: ojs-production
  namespace: ojs-system
spec:
  backend:
    type: redis
    urlSecretRef:
      name: redis-credentials
      key: url
  replicas: 3
  image: ghcr.io/openjobspec/ojs-backend-redis:v0.1.0
  resources:
    requests:
      cpu: "250m"
      memory: "256Mi"
    limits:
      cpu: "1"
      memory: "512Mi"
  autoScaling:
    enabled: true
    minReplicas: 3
    maxReplicas: 10
    targetQueueDepth: 100
  monitoring:
    enabled: true
    serviceMonitor: true
    grafanaDashboard: true
```

### Multi-Worker Deployment

An OJSCluster with multiple specialized workers:

```yaml
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSCluster
metadata:
  name: ojs-multi
spec:
  backend:
    type: redis
    embedded: true
  replicas: 2
---
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSWorker
metadata:
  name: email-worker
spec:
  clusterRef: ojs-multi
  jobTypes: [email.send, email.digest]
  queues: [emails]
  concurrency: 10
  replicas: 2
  image: myapp/email-worker:latest
  autoScaling:
    enabled: true
    minReplicas: 1
    maxReplicas: 10
    targetJobsPerWorker: 5
---
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSWorker
metadata:
  name: data-processor
spec:
  clusterRef: ojs-multi
  jobTypes: [data.transform, report.generate]
  queues: [data-processing]
  concurrency: 50
  replicas: 3
  image: myapp/data-worker:latest
  resources:
    requests:
      cpu: "500m"
      memory: "512Mi"
    limits:
      cpu: "2"
      memory: "2Gi"
```

More examples are available in [`config/samples/`](config/samples/).

## Development

### Build and Test

```bash
make build          # Build operator binary to bin/manager
make test           # Run tests with race detection and coverage
make lint           # Run go vet
```

### Run Locally

```bash
# Install CRDs into cluster
make install

# Run the operator locally (requires kubeconfig)
make run
```

### Docker

```bash
make docker-build   # Build container image
make docker-push    # Push to registry
```

### Helm

```bash
make helm-package   # Package Helm chart
make helm-install   # Install via Helm into ojs-system namespace
make helm-uninstall # Uninstall Helm release
```

### Code Generation

```bash
make generate       # Run go generate (deepcopy, etc.)
```

## Troubleshooting

### Operator pod is not starting

```bash
# Check operator logs
kubectl logs -n ojs-system -l control-plane=controller-manager

# Check events
kubectl get events -n ojs-system --sort-by='.lastTimestamp'
```

### OJSCluster stuck in Pending

```bash
# Check the cluster status and conditions
kubectl describe ojscluster <name>

# If using embedded Redis, verify the Redis pod is running
kubectl get pods -l app.kubernetes.io/component=backend
```

### OJSWorker in Error phase

This typically means the referenced OJSCluster was not found:

```bash
# Verify the clusterRef matches an existing OJSCluster in the same namespace
kubectl get ojsclusters

# Check worker conditions
kubectl describe ojsworker <name>
```

### RBAC errors

```bash
# Verify the ClusterRole has the necessary permissions
kubectl get clusterrole ojs-operator-manager-role -o yaml

# Check if the ServiceAccount is bound correctly
kubectl get clusterrolebinding ojs-operator-manager-rolebinding -o yaml
```

### Resetting a stuck resource

```bash
# Remove the finalizer to allow deletion
kubectl patch ojscluster <name> -p '{"metadata":{"finalizers":null}}' --type=merge
```

## Production Checklist

- [ ] Set resource limits on all OJS server and worker pods
- [ ] Configure `PodDisruptionBudget` for high-availability
- [ ] Enable Prometheus metrics collection (`monitoring.enabled: true`)
- [ ] Set up Grafana dashboards (`monitoring.grafanaDashboard: true`)
- [ ] Use `urlSecretRef` instead of plaintext URLs for backend credentials
- [ ] Configure graceful shutdown on all workers
- [ ] Review and tighten RBAC permissions
- [ ] Test failover and recovery procedures
- [ ] Configure backup strategy for the backend database

## License

Apache License 2.0 — see [LICENSE](../LICENSE) for details.

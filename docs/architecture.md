# Architecture

This document describes the internal architecture of the OJS Kubernetes Operator.

## Overview

The OJS Kubernetes Operator manages the lifecycle of OJS (Open Job Spec) server clusters and worker deployments on Kubernetes. It follows the standard Kubernetes operator pattern using [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime).

```
┌─────────────────────────────────────────────────┐
│                 OJS Operator                     │
│                                                  │
│  ┌──────────────────┐  ┌──────────────────────┐  │
│  │ OJSCluster       │  │ OJSWorker            │  │
│  │ Reconciler       │  │ Reconciler           │  │
│  │                  │  │                      │  │
│  │ ► Deployment     │  │ ► Deployment         │  │
│  │ ► Service        │  │ ► HPA (optional)     │  │
│  │ ► ConfigMap      │  │                      │  │
│  │ ► ServiceMonitor │  │                      │  │
│  │ ► Redis (embed)  │  │                      │  │
│  └──────────────────┘  └──────────────────────┘  │
│                                                  │
│  ┌──────────────────┐  ┌──────────────────────┐  │
│  │ Validating       │  │ Validating           │  │
│  │ Webhook          │  │ Webhook              │  │
│  │ (OJSCluster)     │  │ (OJSWorker)          │  │
│  └──────────────────┘  └──────────────────────┘  │
└─────────────────────────────────────────────────┘
```

## Custom Resource Definitions

### OJSCluster

Represents an OJS server deployment. When you create an `OJSCluster`, the operator creates:

1. **ConfigMap** (`<name>-config`) — Contains `BACKEND_TYPE` and `BACKEND_URL`
2. **Deployment** (`<name>-server`) — OJS server pods with health/readiness probes
3. **Service** (`<name>-server`) — Exposes HTTP (8080) and metrics (9090) ports
4. **ServiceMonitor** (`<name>-server`, optional) — Prometheus scrape configuration
5. **Embedded Redis** (`<name>-redis`, optional) — Redis Deployment + Service

### OJSWorker

Represents a worker deployment that processes jobs from an OJSCluster. When you create an `OJSWorker`, the operator creates:

1. **Deployment** (`<name>`) — Worker pods with OJS connection environment variables
2. **HPA** (`<name>-hpa`, optional) — Horizontal Pod Autoscaler for auto-scaling

## Reconciliation Loop

Both controllers follow the same reconciliation pattern:

```
1. Fetch the custom resource
2. Handle deletion (remove finalizer)
3. Add finalizer if missing
4. Reconcile child resources (create or update)
5. Update status (phase, conditions, replicas)
```

### OJSCluster Reconciler Flow

```
Reconcile(OJSCluster)
  ├── Handle deletion → remove finalizer
  ├── Add finalizer
  ├── Set initial phase (Pending)
  ├── reconcileEmbeddedBackend()  [if embedded: true]
  ├── reconcileConfigMap()
  ├── reconcileDeployment()
  ├── reconcileService()
  ├── reconcileServiceMonitor()   [if monitoring.serviceMonitor: true]
  └── updateStatus()
       ├── Read Deployment status
       ├── Set phase (Pending/Running/Scaling)
       └── Set conditions (Ready, Available, Progressing, Degraded)
```

### OJSWorker Reconciler Flow

```
Reconcile(OJSWorker)
  ├── Handle deletion → remove finalizer
  ├── Add finalizer
  ├── Resolve parent OJSCluster
  │    └── If not found → set phase=Error, requeue after 30s
  ├── reconcileWorkerDeployment()
  ├── reconcileWorkerHPA()
  │    ├── If autoscaling enabled → create/update HPA
  │    └── If autoscaling disabled → delete HPA if exists
  └── updateWorkerStatus()
       ├── Read Deployment status
       ├── Set phase (Pending/Running/Scaling)
       └── Set conditions (Ready, Available, Progressing, Degraded)
```

## Ownership and Garbage Collection

All child resources (Deployments, Services, ConfigMaps, HPAs, ServiceMonitors) are created with an **owner reference** pointing to the parent CRD. This means:

- When an `OJSCluster` is deleted, Kubernetes garbage collection automatically removes all its child resources
- When an `OJSWorker` is deleted, its Deployment and HPA are automatically removed
- The operator uses **finalizers** to ensure cleanup logic runs before the CRD is deleted

## Status Conditions

Both CRDs maintain a set of standard status conditions:

| Condition | Description |
|-----------|-------------|
| `Ready` | All desired replicas are running and ready |
| `Available` | At least one replica is serving traffic |
| `Progressing` | A rollout, scale, or initial deployment is in progress |
| `Degraded` | Fewer replicas are ready than desired |

Each condition tracks `observedGeneration` so clients can determine whether the status reflects the latest spec changes.

## Event Recording

The operator records Kubernetes events for important state transitions:

- **Reconciling** — Initial reconciliation starts
- **PhaseChanged** — Cluster/worker transitions between phases
- **BackendFailed** — Embedded backend failed to deploy
- **ClusterNotFound** — Worker references a non-existent cluster
- **Deleting** — Cleanup of child resources begins

View events with:

```bash
kubectl describe ojscluster my-ojs
kubectl get events --field-selector involvedObject.name=my-ojs
```

## Webhooks

The operator includes validating webhooks for both CRDs:

### OJSCluster Validation
- `backend.type` must be one of: `redis`, `postgres`, `nats`, `kafka`, `sqs`, `lite`
- `replicas` must be ≥ 1 (if set)
- `autoScaling.maxReplicas` must be ≥ `autoScaling.minReplicas`

### OJSWorker Validation
- `clusterRef` is required
- `image` is required
- `jobTypes` must contain at least one non-empty entry
- `concurrency` must be ≥ 0
- `autoScaling.minReplicas` must be ≥ 1

Webhooks can be disabled by setting `ENABLE_WEBHOOKS=false` on the operator.

## ServiceMonitor Integration

When `monitoring.enabled: true` and `monitoring.serviceMonitor: true`, the operator creates a `ServiceMonitor` (from the prometheus-operator CRD) that:

- Selects the OJS server service by label
- Scrapes the `/metrics` endpoint on port `metrics` (9090)
- Uses a default scrape interval of 30 seconds

This requires the prometheus-operator CRDs to be installed in the cluster. If the CRDs are not available, the ServiceMonitor creation will fail gracefully and log a warning.

## Project Layout

```
ojs-k8s-operator/
├── api/v1alpha1/          # CRD type definitions and deepcopy
├── cmd/manager/           # Operator entry point
├── internal/
│   ├── controller/        # Reconciler implementations
│   └── webhook/           # Admission webhook validators
├── config/
│   ├── crd/               # CRD YAML manifests
│   ├── rbac/              # RBAC (ClusterRole, binding, SA)
│   ├── manager/           # Operator Deployment manifest
│   └── samples/           # Example CR manifests
├── charts/ojs-operator/   # Helm chart
└── docs/                  # Documentation
```

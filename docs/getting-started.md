# Getting Started

This guide walks you through installing the OJS Kubernetes Operator and deploying your first OJS cluster with workers.

## Prerequisites

- Kubernetes 1.26+
- `kubectl` configured to access your cluster
- Helm 3.x (for Helm-based installation)

## Installation

### Option 1: Helm (Recommended)

```bash
helm install ojs-operator ./charts/ojs-operator \
  --namespace ojs-system --create-namespace
```

The Helm chart enables leader election and validating webhooks by default. With
the default settings, cert-manager provisions the webhook serving certificate.
Disable admission webhooks with `--set webhook.enabled=false` if certificate
provisioning is unavailable.

Verify the operator is running:

```bash
kubectl get pods -n ojs-system
```

### Option 2: Manual Manifests

```bash
# Install CRDs
kubectl apply -f config/crd/

# Install RBAC and operator
kubectl apply -f config/rbac/
kubectl apply -f config/manager/deployment.yaml
```

The raw manager Deployment keeps leader election enabled, but explicitly sets
`ENABLE_WEBHOOKS=false` because these manifests do not provision or mount
serving certificates. To opt in, supply and mount valid webhook TLS material,
create the required admission webhook resources, set `WEBHOOK_CERT_DIR` to the
mount path, and change `ENABLE_WEBHOOKS` to `true`.

## Deploy Your First OJS Cluster

### 1. Create an OJSCluster with External Redis

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

```bash
kubectl apply -f - <<EOF
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSCluster
metadata:
  name: my-ojs
spec:
  backend:
    type: redis
    url: "redis://redis:6379"
  replicas: 2
EOF
```

### 2. Create an OJSCluster with Embedded Redis

For development or testing, the operator can deploy a Redis instance automatically:

```yaml
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSCluster
metadata:
  name: dev-ojs
spec:
  backend:
    type: redis
    embedded: true
  replicas: 1
```

### 3. Check Cluster Status

```bash
kubectl get ojsclusters
```

Output:

```
NAME     BACKEND   REPLICAS   READY   PHASE     AGE
my-ojs   redis     2          2       Running   30s
```

### 4. View Detailed Status

```bash
kubectl describe ojscluster my-ojs
```

Look for the `Conditions` section which shows `Ready`, `Available`, `Progressing`, and `Degraded` status.

## Deploy Your First Worker

### 1. Create an OJSWorker

```yaml
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSWorker
metadata:
  name: email-worker
spec:
  clusterRef: my-ojs
  jobTypes:
    - email.send
    - email.verify
  queues:
    - default
  concurrency: 10
  replicas: 2
  image: my-registry/email-worker:latest
```

```bash
kubectl apply -f - <<EOF
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSWorker
metadata:
  name: email-worker
spec:
  clusterRef: my-ojs
  jobTypes:
    - email.send
  queues:
    - default
  concurrency: 5
  replicas: 2
  image: my-registry/email-worker:latest
EOF
```

### 2. Check Worker Status

```bash
kubectl get ojsworkers
```

Output:

```
NAME           CLUSTER   REPLICAS   READY   QUEUE DEPTH   PHASE     AGE
email-worker   my-ojs    2          2       0             Running   15s
```

## Enable Auto-Scaling

```yaml
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSWorker
metadata:
  name: email-worker
spec:
  clusterRef: my-ojs
  jobTypes:
    - email.send
  image: my-registry/email-worker:latest
  autoScaling:
    enabled: true
    minReplicas: 1
    maxReplicas: 20
    targetJobsPerWorker: 5
```

## Enable Monitoring

To enable Prometheus metrics scraping and ServiceMonitor creation (requires prometheus-operator):

```yaml
apiVersion: ojs.openjobspec.dev/v1alpha1
kind: OJSCluster
metadata:
  name: my-ojs
spec:
  backend:
    type: redis
    url: "redis://redis:6379"
  monitoring:
    enabled: true
    serviceMonitor: true
```

## Cleanup

```bash
kubectl delete ojsworker email-worker
kubectl delete ojscluster my-ojs
```

## Next Steps

- [Configuration Reference](configuration.md) — All CRD fields documented
- [Architecture](architecture.md) — How the operator works internally
- [Troubleshooting](troubleshooting.md) — Common issues and solutions

# CRD Reference

Detailed field reference for the OJS Kubernetes Operator Custom Resource Definitions.

## OJSCluster

**API Version:** `ojs.openjobspec.dev/v1alpha1`
**Kind:** `OJSCluster`
**Short Name:** `ojs`

### Spec

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `backend` | [BackendSpec](#backendspec) | **Yes** | — | — | Backend storage configuration |
| `replicas` | *int32 | No | `2` | minimum: 1 | Number of OJS server replicas |
| `image` | string | No | `ghcr.io/openjobspec/ojs-server:v0.5.0` | — | OJS server container image |
| `autoScaling` | *[AutoScalingSpec](#autoscalingspec) | No | — | — | Server auto-scaling configuration |
| `resources` | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.28/#resourcerequirements-v1-core) | No | — | — | CPU/memory resource requests and limits |
| `monitoring` | *[MonitoringSpec](#monitoringspec) | No | — | — | Prometheus monitoring configuration |

### BackendSpec

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `type` | string | **Yes** | — | enum: `redis`, `postgres`, `nats`, `kafka`, `sqs`, `lite` | Backend type |
| `url` | string | No | — | — | Connection URL (e.g., `redis://redis:6379`) |
| `urlSecretRef` | *[SecretKeyRef](#secretkeyref) | No | — | — | Reference to a Secret containing the URL |
| `embedded` | bool | No | `false` | — | Auto-deploy the backend (only `redis` supported) |

> **Note:** Specify exactly one of `url`, `urlSecretRef`, or `embedded: true`.

### SecretKeyRef

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | **Yes** | Name of the Kubernetes Secret |
| `key` | string | **Yes** | Key within the Secret |

### AutoScalingSpec

| Field | Type | Required | Validation | Description |
|-------|------|----------|------------|-------------|
| `enabled` | bool | **Yes** | — | Enable auto-scaling |
| `minReplicas` | int32 | **Yes** | minimum: 1 | Minimum number of replicas |
| `maxReplicas` | int32 | **Yes** | minimum: 1 | Maximum number of replicas (must be ≥ minReplicas) |
| `targetQueueDepth` | int64 | **Yes** | minimum: 1 | Desired queue depth per replica |
| `targetJobsPerWorker` | int64 | No | — | Desired active jobs per worker |
| `scaleUpCooldown` | string | No | — | Cooldown after scale-up (e.g., `60s`) |
| `scaleDownCooldown` | string | No | — | Cooldown after scale-down (e.g., `300s`) |

### MonitoringSpec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | **Yes** | — | Enable Prometheus metrics endpoint |
| `serviceMonitor` | bool | No | `false` | Create a Prometheus ServiceMonitor resource |
| `grafanaDashboard` | bool | No | `false` | Create a Grafana dashboard ConfigMap |

### Status

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current phase: `Pending`, `Running`, `Scaling`, `Error` |
| `replicas` | int32 | Total server pod count |
| `readyReplicas` | int32 | Ready server pod count |
| `queueDepth` | int64 | Current number of queued jobs |
| `activeJobs` | int64 | Current number of active jobs |
| `conditions` | []Condition | Standard Kubernetes conditions |

### Status Conditions

| Type | Description |
|------|-------------|
| `Ready` | `True` when all replicas are ready |
| `Available` | `True` when at least one replica is serving traffic |
| `Progressing` | `True` during rollout, scaling, or initial deployment |
| `Degraded` | `True` when fewer replicas are ready than desired |
| `BackendReady` | `True` when the embedded backend is running (only set for embedded backends) |

### Printer Columns

When using `kubectl get ojsclusters`:

```
NAME     BACKEND   REPLICAS   READY   PHASE     AGE
my-ojs   redis     2          2       Running   5m
```

---

## OJSWorker

**API Version:** `ojs.openjobspec.dev/v1alpha1`
**Kind:** `OJSWorker`
**Short Name:** `ojsw`

### Spec

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `clusterRef` | string | **Yes** | — | minLength: 1 | Name of the OJSCluster this worker connects to |
| `jobTypes` | []string | **Yes** | — | minItems: 1 | Job types this worker handles |
| `queues` | []string | No | `["default"]` | — | Queues this worker processes |
| `concurrency` | int32 | No | `0` (unlimited) | minimum: 0 | Concurrent jobs per worker pod |
| `replicas` | *int32 | No | `1` | minimum: 0 | Desired worker pod count |
| `image` | string | **Yes** | — | minLength: 1 | Worker container image |
| `command` | []string | No | — | — | Container command override |
| `env` | []EnvVar | No | — | — | Additional environment variables |
| `resources` | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.28/#resourcerequirements-v1-core) | No | — | — | CPU/memory resource requests and limits |
| `autoScaling` | *[WorkerAutoScalingSpec](#workerautoscalingspec) | No | — | — | Queue-based auto-scaling |
| `gracefulShutdown` | *[GracefulShutdownSpec](#gracefulshutdownspec) | No | — | — | Graceful shutdown behavior |

### WorkerAutoScalingSpec

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `enabled` | bool | **Yes** | — | — | Enable auto-scaling |
| `minReplicas` | int32 | **Yes** | — | minimum: 1 | Minimum worker pods |
| `maxReplicas` | int32 | **Yes** | — | minimum: 1 | Maximum worker pods (must be ≥ minReplicas) |
| `targetJobsPerWorker` | int64 | **Yes** | — | minimum: 1 | Desired pending jobs per worker replica |
| `scaleUpThreshold` | int64 | No | — | — | Queue depth threshold to trigger scale-up |
| `scaleDownDelay` | string | No | — | — | Delay before scale-down (e.g., `5m`) |
| `pollingInterval` | string | No | `30s` | — | Interval to check queue metrics |

### GracefulShutdownSpec

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `timeoutSeconds` | int32 | No | `30` | minimum: 0 | Maximum time to wait for active jobs |
| `drainBeforeShutdown` | bool | No | `false` | — | Wait for active jobs before terminating |

### Status

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current phase: `Pending`, `Running`, `Scaling`, `Draining`, `Error` |
| `replicas` | int32 | Total worker pod count |
| `readyReplicas` | int32 | Ready worker pod count |
| `activeJobs` | int64 | Jobs being processed |
| `queueDepth` | int64 | Pending job count for worker's queues |
| `lastScaleTime` | *Time | When the last scaling event occurred |
| `conditions` | []Condition | Standard Kubernetes conditions |

### Subresources

- **Status** — Enables `kubectl get ojsworker <name> -o jsonpath='{.status}'`
- **Scale** — Enables `kubectl scale ojsworker <name> --replicas=N`

### Printer Columns

When using `kubectl get ojsworkers`:

```
NAME           CLUSTER   REPLICAS   READY   QUEUE DEPTH   PHASE     AGE
email-worker   my-ojs    2          2       0             Running   5m
```

---

## Injected Environment Variables

### Worker Pods

The operator injects these environment variables into all worker pods:

| Variable | Description | Example |
|----------|-------------|---------|
| `OJS_URL` | URL of the OJS server | `http://my-ojs-server.default.svc.cluster.local:8080` |
| `OJS_QUEUES` | Comma-separated queues | `default,high` |
| `OJS_JOB_TYPES` | Comma-separated job types | `email.send,report.generate` |
| `OJS_CONCURRENCY` | Concurrency (if > 0) | `10` |

### Server Pods

The operator injects backend-specific variables into server pods:

| Backend | Variable | Example |
|---------|----------|---------|
| `redis` | `REDIS_URL` | `redis://redis:6379` |
| `postgres` | `DATABASE_URL` | `postgres://user:pass@host:5432/ojs` |
| `nats` | `NATS_URL` | `nats://nats:4222` |
| `kafka` | `KAFKA_BROKERS` | `kafka:9092` |
| `sqs` | `SQS_QUEUE_URL` | `https://sqs.us-east-1.amazonaws.com/...` |
| `lite` | `BACKEND_URL` | `file:///data/ojs.db` |

---

## Naming Conventions

Child resources created by the operator follow these naming patterns:

### OJSCluster Resources

| Resource | Name Pattern |
|----------|-------------|
| ConfigMap | `<cluster-name>-config` |
| Deployment | `<cluster-name>-server` |
| Service | `<cluster-name>-server` |
| ServiceMonitor | `<cluster-name>-server` |
| Embedded Redis Deployment | `<cluster-name>-redis` |
| Embedded Redis Service | `<cluster-name>-redis` |

### OJSWorker Resources

| Resource | Name Pattern |
|----------|-------------|
| Deployment | `<worker-name>` |
| HPA | `<worker-name>-hpa` |

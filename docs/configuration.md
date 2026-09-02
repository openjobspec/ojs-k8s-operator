# Configuration Reference

Complete reference for all CRD fields in the OJS Kubernetes Operator.

## OJSCluster

The `OJSCluster` resource manages the OJS server deployment, service, and optional embedded backend.

### spec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `backend` | [BackendSpec](#backendspec) | **Yes** | — | Backend storage configuration |
| `replicas` | int32 | No | `2` | Number of OJS server replicas |
| `image` | string | No | `ghcr.io/openjobspec/ojs-server:v0.5.0` | OJS server container image |
| `autoScaling` | [AutoScalingSpec](#autoscalingspec) | No | — | Server auto-scaling configuration |
| `resources` | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.28/#resourcerequirements-v1-core) | No | — | CPU/memory resource requests and limits |
| `monitoring` | [MonitoringSpec](#monitoringspec) | No | — | Prometheus monitoring configuration |
| `podDisruptionBudget` | [PDBSpec](#pdbspec) | No | enabled for replicas > 1 | Server disruption-budget configuration |

### BackendSpec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `type` | string | **Yes** | — | Backend type: `redis`, `postgres`, `nats`, `kafka`, `sqs`, or `lite` |
| `url` | string | No | — | Connection URL (e.g., `redis://redis:6379`) |
| `urlSecretRef` | [SecretKeyRef](#secretkeyref) | No | — | Reference to a Secret containing the connection URL |
| `embedded` | bool | No | `false` | Auto-deploy the backend (currently only `redis` supported) |

> **Note:** Exactly one of `url`, `urlSecretRef`, or `embedded: true` should be specified.

### SecretKeyRef

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | **Yes** | Name of the Kubernetes Secret |
| `key` | string | **Yes** | Key within the Secret |

### AutoScalingSpec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | **Yes** | — | Enable auto-scaling |
| `minReplicas` | int32 | **Yes** | — | Minimum number of replicas (≥ 1) |
| `maxReplicas` | int32 | **Yes** | — | Maximum number of replicas (≥ minReplicas) |
| `targetQueueDepth` | int64 | **Yes** | — | Desired queue depth per replica |
| `targetJobsPerWorker` | int64 | No | — | Desired active jobs per worker |
| `scaleUpCooldown` | string | No | — | Cooldown after scale-up (e.g., `60s`) |
| `scaleDownCooldown` | string | No | — | Cooldown after scale-down (e.g., `300s`) |

### MonitoringSpec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | **Yes** | — | Enable Prometheus metrics endpoint |
| `serviceMonitor` | bool | No | `false` | Create a Prometheus ServiceMonitor resource |
| `grafanaDashboard` | bool | No | `false` | Create a Grafana dashboard ConfigMap |

### PDBSpec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | No | replicas > 1 | Create a server PDB; disabling it or reducing replicas to 1 removes only an operator-owned PDB |
| `minAvailable` | int32 | No | — | Minimum number of available server pods |
| `maxUnavailable` | int32 | No | `1` | Maximum unavailable pods when `minAvailable` is unset |

### status

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current phase: `Pending`, `Running`, `Scaling`, `Error` |
| `replicas` | int32 | Total server pod count |
| `readyReplicas` | int32 | Ready server pod count |
| `queueDepth` | int64 | Current number of queued jobs |
| `activeJobs` | int64 | Current number of active jobs |
| `conditions` | []Condition | Status conditions (see below) |

### Status Conditions

| Type | Description |
|------|-------------|
| `Ready` | `True` when all replicas are ready |
| `Available` | `True` when at least one replica is serving traffic |
| `Progressing` | `True` during rollout, scaling, or initial deployment |
| `Degraded` | `True` when fewer replicas are ready than desired |
| `BackendReady` | `True` when the embedded backend is running (only set for embedded backends) |

Each condition includes `observedGeneration` to track which generation of the spec the condition reflects.

---

## OJSWorker

The `OJSWorker` resource manages worker deployments and optional HPA for queue-depth-based auto-scaling.

### spec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `clusterRef` | string | **Yes** | — | Name of the OJSCluster this worker connects to |
| `jobTypes` | []string | **Yes** | — | Job types this worker handles |
| `queues` | []string | No | `["default"]` | Queues this worker processes |
| `concurrency` | int32 | No | `0` (unlimited) | Concurrent jobs per worker pod |
| `replicas` | int32 | No | `1` | Desired count without autoscaling; initial HPA size when autoscaling is enabled |
| `image` | string | **Yes** | — | Worker container image |
| `command` | []string | No | — | Container command override |
| `env` | []EnvVar | No | — | Additional environment variables |
| `resources` | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.28/#resourcerequirements-v1-core) | No | — | CPU/memory resource requests and limits |
| `autoScaling` | [WorkerAutoScalingSpec](#workerautoscalingspec) | No | — | Queue-based auto-scaling |
| `gracefulShutdown` | [GracefulShutdownSpec](#gracefulshutdownspec) | No | — | Graceful shutdown behavior |

### WorkerAutoScalingSpec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | **Yes** | — | Enable auto-scaling |
| `minReplicas` | int32 | **Yes** | — | Minimum worker pods (≥ 1) |
| `maxReplicas` | int32 | **Yes** | — | Maximum worker pods (≥ minReplicas) |
| `targetJobsPerWorker` | int64 | **Yes** | — | Desired pending jobs per worker replica |
| `scaleUpThreshold` | int64 | No | — | Queue depth threshold to trigger scale-up |
| `scaleDownDelay` | string | No | — | Delay before scale-down (e.g., `5m`) |
| `pollingInterval` | string | No | `30s` | Interval to check queue metrics |

While `autoScaling.enabled` is true, the HPA owns the Deployment replica count and normal
reconciliation preserves it. Disabling autoscaling deletes the HPA and resumes replica
management from `spec.replicas`.

### GracefulShutdownSpec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `timeoutSeconds` | int32 | No | `30` | Maximum time to wait for active jobs |
| `drainBeforeShutdown` | bool | No | `false` | Wait for active jobs before terminating |

### status

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current phase: `Pending`, `Running`, `Scaling`, `Draining`, `Error` |
| `replicas` | int32 | Total worker pod count |
| `readyReplicas` | int32 | Ready worker pod count |
| `activeJobs` | int64 | Jobs being processed |
| `queueDepth` | int64 | Pending job count for worker's queues |
| `lastScaleTime` | Time | When the last scaling event occurred |
| `conditions` | []Condition | Status conditions: `Ready`, `Available`, `Progressing`, `Degraded` |

---

## Environment Variables (Injected into Workers)

The operator automatically injects these environment variables into worker pods:

| Variable | Description |
|----------|-------------|
| `OJS_URL` | URL of the OJS server (e.g., `http://my-ojs-server.default.svc.cluster.local:8080`) |
| `OJS_QUEUES` | Comma-separated list of queues |
| `OJS_JOB_TYPES` | Comma-separated list of job types |
| `OJS_CONCURRENCY` | Concurrency setting (if > 0) |

## Backend Environment Variables (Injected into Server)

| Backend | Variable | Example |
|---------|----------|---------|
| `redis` | `REDIS_URL` | `redis://redis:6379` |
| `postgres` | `DATABASE_URL` | `postgres://user:pass@host:5432/ojs` |
| `nats` | `NATS_URL` | `nats://nats:4222` |
| `kafka` | `KAFKA_BROKERS` | `kafka:9092` |
| `sqs` | `SQS_QUEUE_URL` | `https://sqs.us-east-1.amazonaws.com/...` |
| `lite` | `BACKEND_URL` | `file:///data/ojs.db` |

# ojs-k8s-operator — Clean Code / SRP Refactor Audit

**Branch:** `refactor/clean-code-srp`
**Scope:** `ojs-k8s-operator` module only (standalone Go module; sibling repositories in this
monorepo were not touched).
**Date:** 2026-08-05

This document records the scope, findings, actions taken, file-level evidence, and exact
verification commands/results for the Single-Responsibility-Principle (SRP) refactor and
targeted bug audit of the OJS Kubernetes operator.

---

## 1. Scope

1. Split `OJSCluster` lifecycle/status/controller-registration from desired child-resource policy.
2. Split `OJSWorker` lifecycle orchestration, Deployment projection, HPA policy, status
   transitions, and labels/serialization into actor-owned units.
3. Extract pure desired-state builders from long projection closures where they own real
   Kubernetes policy; add characterization/snapshot tests.
4. Extract shared private condition/status transition policy where cluster/worker behavior is
   exactly identical (control flow), preserving condition order/reasons/messages/phases/
   observed generation.
5. Metrics queue snapshot: deep immutable copies; delete stale queue cache entries; race/alias/
   staleness tests.
6. Keep exported `ValidBackendTypes` compatible while making internal validation race-safe/
   immutable; document any behavior change instead of silently changing semantics.
7. Retain the pre-existing Helm `rbac.yaml` separator fix; review all chart templates for
   whitespace/separator/conditional bugs; add a rendering test matrix; fix clear bugs without
   renaming values.
8. Audit >50-line functions, ignored errors/status updates, and dead code; fix explicit
   controller errors, requeue behavior, HPA deletion, optional ServiceMonitor, and metrics
   transport bugs only where intended behavior is unambiguous.
9. Keep CRD public-schema edits limited to synchronizing the existing
   `spec.podDisruptionBudget` Go API field into both shipped OJSCluster CRDs; no manual edits to
   generated deepcopy code;
   `charts/ojs-operator/templates/rbac.yaml`'s pre-existing unstaged fix and `stash@{0}`
   (`copilot-helm-repair-backup`) preserved exactly, untouched.
10. Remediate follow-up findings for PDB RBAC, complete webhook TLS/startup wiring, HPA replica
    ownership, and ownership-safe PDB cleanup.
11. Remediate the final ownership/schema findings: disabled autoscaling may delete only an HPA
    controlled by the current `OJSWorker`, and PDB availability counts must be non-negative in
    Go admission validation and both shipped CRD schemas.

### Explicit constraints honored

- **`charts/ojs-operator/templates/rbac.yaml`**: the pre-existing unstaged one-line separator
  fix (`{{- if .Values.rbac.create -}}` → `{{ if .Values.rbac.create -}}`) was preserved
  exactly. The file now also contains the separately requested `policy/poddisruptionbudgets`
  permissions; the pre-existing line itself was not reverted or altered.
- **`stash@{0}` (`copilot-helm-repair-backup`)**: never applied, dropped, or inspected beyond a
  read-only `git stash show`. `git stash list` still shows exactly one entry at the end of this
  session.
- **No sibling repositories** in the organization workspace were modified.
- **Targeted CRD schema edits only**: `PDBSpec.MinAvailable` and `MaxUnavailable` gained
  `+kubebuilder:validation:Minimum=0` markers, and both shipped OJSCluster CRDs gained only the
  matching `minimum: 0` constraints within the previously synchronized
  `spec.podDisruptionBudget` schema. No regeneration was needed or performed.
- **No manual edits to `api/v1alpha1/zz_generated_deepcopy.go`.**
- **No values.yaml keys renamed**; comments now also document webhook certificate-secret
  behavior and the operator/chart PDB distinction (see §2.11).

---

## 2. Findings & Actions

### 2.1 OJSCluster split (item 1)

**Before:** a single 766-line `internal/controller/ojscluster_controller.go` mixed lifecycle
orchestration, status/condition transitions, controller registration, and all child-resource
(ConfigMap/Deployment/Service/embedded-Redis/PDB/ServiceMonitor) build-and-apply logic in one
file with many inline closures.

**After**, four cohesive files (all package `controller`):

| File | Responsibility | Lines |
|---|---|---|
| `ojscluster_controller.go` | Lifecycle orchestration (`Reconcile`, finalizer handling, phase bootstrap, embedded-backend gate, child-resource sequencing) + controller registration (`SetupWithManager`) | 193 |
| `ojscluster_status.go` | Condition-type constants, per-kind message wording, `updateStatus`, `setCondition` (delegates transition policy to `status.go`) | 79 |
| `ojscluster_resources.go` | Desired child-resource *reconciliation* (the only place that calls the Kubernetes API to create/update/delete ConfigMap, Deployment, Service, embedded Redis, PDB, ServiceMonitor) | 225 |
| `ojscluster_desired.go` | Pure desired-state *builders* — no I/O, no controller state; deterministic given a `*OJSCluster` | 542 |

`Reconcile` itself was decomposed from a 96-line function into `reconcileFinalizer`,
`ensureInitialPhase`, `reconcileEmbeddedBackendPhase`, and `reconcileChildResources` helper
methods, each independently testable and under 50 lines, while the top-level `Reconcile` is now
~35 lines and preserves the exact original control flow/error/requeue semantics (validated by
the full pre-existing cluster test suite, unchanged in assertions).

### 2.2 OJSWorker split (item 2)

**Before:** a single 348-line `ojsworker_controller.go` mixed lifecycle, Deployment projection,
HPA policy, status transitions, and labels/serialization.

**After**, five actor-owned files:

| File | Responsibility | Lines |
|---|---|---|
| `ojsworker_controller.go` | Lifecycle orchestration (`Reconcile`, finalizer handling, cluster resolution) + registration | 183 |
| `ojsworker_status.go` | Condition aliases, message wording, `updateWorkerStatus`, `setWorkerCondition` | 77 |
| `ojsworker_deployment.go` | Deployment *projection* reconciliation (`reconcileWorkerDeployment`) | 33 |
| `ojsworker_hpa.go` | HPA *policy* reconciliation, including corrected deletion semantics (§2.6) | 57 |
| `ojsworker_desired.go` | Pure builders: env vars, container, pod spec, HPA spec, `labelsForWorker`, `joinStrings` serialization | 152 |

`Reconcile` was decomposed into `reconcileFinalizer`, `resolveCluster`, and
`autoScalingRequeueResult` helpers, dropping from 81 lines to ~30 while preserving byte-identical
`ctrl.Result`/error semantics for every branch (verified by existing + new tests).

### 2.3 Pure desired-state builders + characterization tests (item 3)

Extracted from the former inline `CreateOrUpdate` closures into standalone, side-effect-free
functions:

- Cluster: `applyServerDeploymentSpec`, `desiredServerContainer` (+ `desiredServerLivenessProbe`/
  `ReadinessProbe`/`StartupProbe`/`Resources`), `desiredServerEnvVars`,
  `desiredServerContainerSecurityContext`, `buildPodSecurityContext`, `desiredServerPodSpec`,
  `desiredServerServiceSpec`, `desiredConfigMapData`, `applyRedisDeploymentSpec` (+
  `redisContainer`/`redisPodSecurityContext`), `desiredRedisServiceSpec`, `pdbDisabled`,
  `desiredPDBSpec`, `desiredServiceMonitorSpec`, `resolveBackendURL`, `backendURLEnvVar`,
  `labelsForCluster`.
- Worker: `applyWorkerDeploymentSpec`, `desiredWorkerContainer`, `desiredWorkerEnvVars`,
  `workerReplicas`, `workerTerminationGracePeriod`, `desiredHPASpec`, `labelsForWorker`,
  `joinStrings`.

New characterization/snapshot tests (`ojscluster_desired_test.go`,
`ojsworker_desired_test.go`, 44 new test functions total) pin: resource **names** (`-server`,
`-config`, `-redis`, `-hpa` suffixes), **labels** (all `app.kubernetes.io/*` + operator-specific
keys), **probes** (paths, ports, delays, thresholds), **images** (default vs. override),
**resources** (default requests/limits vs. override), **security** (pod/container security
context defaults and per-field overrides), **topology** (default spread/anti-affinity for
replicas>1, single-replica no-op, custom-constraint override), and **serialization**
(`joinStrings`, env var formatting, ConfigMap data, HPA spec, PDB min/max-unavailable
precedence). Owner-reference wiring (`SetControllerReference`) is intentionally left to the
existing client-backed reconcile tests (e.g. `TestWorkerReconcile_OwnerReferences`), since it is
inherently not a pure function.

### 2.4 Shared status/condition transition policy (item 4)

`internal/controller/status.go` introduces `applyDeploymentStatus`, which contains the exact
phase-decision / condition-ordering / `ObservedGeneration` logic that was previously duplicated
byte-for-byte between `OJSClusterReconciler.updateStatus` and
`OJSWorkerReconciler.updateWorkerStatus` (confirmed identical control flow via line-by-line
diff before extraction). Per-kind **wording** (e.g. "server" vs. "worker" in messages, which
differs — see below) is supplied via a `deploymentStatusMessages` struct
(`clusterStatusMessages` / `workerStatusMessages`), so message text is preserved exactly:

- Cluster "no ready replicas" Ready-condition message: `"No replicas are ready yet"` (no noun).
- Worker equivalent: `"No worker replicas are ready yet"` (includes "worker").

These two message sets are **not** textually uniform substitutions of one noun (the original
code had this asymmetry already), so the shared function takes fully-formed message strings
rather than trying to template a single noun — this preserves the exact historical wording for
both kinds without inventing new text.

`condReady`/`condAvailable`/`condProgressing`/`condDegraded` (cluster) and
`condWorkerReady`/`condWorkerAvail`/`condWorkerProgress`/`condWorkerDegraded` (worker) are kept
as separate exported... (well, unexported) identifiers for call-site readability and
backward-compat with existing tests, but the worker constants are now literal aliases of the
cluster constants (`condWorkerReady = condReady`, etc.) to make the shared string vocabulary
explicit.

All 12 status-transition tests in `status_test.go` (pre-existing, covering Pending/Running/
Scaling phases and all four conditions for both kinds) pass unmodified against the shared
implementation.

### 2.5 Metrics queue snapshot immutability & staleness (item 5)

**Bugs found in `internal/metrics/queue_metrics.go`:**

1. `GetAllMetrics()` returned a *shallow* copy of the map — the `*QueueMetrics` **pointers**
   were shared with the collector's internal cache. Any caller mutating a returned
   `*QueueMetrics` (e.g. a Prometheus exporter enriching a struct in place) would have silently
   corrupted the collector's own state, visible to every subsequent `GetQueueDepth`/
   `GetAllMetrics` call and racy under concurrent `Poll`.
2. `Poll()` only ever **added/overwrote** map entries; a queue that disappeared from the
   upstream `/ojs/v1/queues` response (deleted, renamed) was never removed from the cache,
   leaking memory indefinitely and — if this collector is ever wired to a Prometheus registry —
   leaving stale time series behind forever.

**Fix:** `GetAllMetrics` now returns a deep copy (`copied := *v; result[k] = &copied`) per entry;
`Poll` builds a fresh map from the latest response and atomically swaps it in under the lock
(`c.metrics = fresh`), which both fixes (1) at the source and evicts stale entries for (2) —
using only the collector's existing API surface (no new exported methods).

**Tests added** (`queue_metrics_test.go`): `TestGetAllMetrics_ReturnsDeepCopy`,
`TestGetAllMetrics_ReturnedMapIsIndependent` (alias tests — mutating/deleting/injecting into a
returned map/value must never affect the collector), `TestPoll_EvictsStaleQueues` (staleness),
`TestPoll_FailedPollDoesNotEvictExistingData` (a failed poll must not wipe good cached data),
`TestCollector_ConcurrentPollAndReadRace` (race test: concurrent `Poll` + `GetAllMetrics`
mutation of returned copies + `GetQueueDepth`, run under `-race`).

Minor cleanup in the same file: `resp.Body.Close()` error is now explicitly discarded
(`defer func() { _ = resp.Body.Close() }()`) to satisfy `errcheck`; `Poll` was split to extract
`fetchQueueStats` (HTTP round-trip + decode) from the cache-swap logic, bringing it from 52 to
under 50 lines per function.

### 2.6 `ValidBackendTypes` race-safety (item 6)

**Before:** `validateOJSCluster` read the exported, mutable package-level
`var ValidBackendTypes = map[string]bool{...}` directly on every admission request. A mutable
map with no synchronization, readable/writable from any importer, is a data race waiting to
happen the moment any caller ever wrote to it concurrently with validation.

**Fix:** `ValidBackendTypes` remains exported with its original name, type, and default contents
for backward-compatible introspection, but `validateOJSCluster` now checks against
`validBackendTypes` — an unexported `map[string]struct{}` built once from `ValidBackendTypes` at
package-init time and never written to again. This is a **documented, intentional behavior
change**: mutating `ValidBackendTypes` at runtime no longer has any effect on validation
(previously it silently did, unsafely). The doc comment on `ValidBackendTypes` states this
explicitly.

**Tests added:** `TestValidateOJSCluster_ExportedMapMutationHasNoEffect` (proves mutation of the
exported map no longer changes validation outcomes, in both directions — adding a bogus entry
and removing a valid one), `TestValidateOJSCluster_ConcurrentValidationRace` (20 goroutines
calling `validateOJSCluster` concurrently, run under `-race`).

### 2.7 Helm chart review (item 7)

- **Preserved** the pre-existing `rbac.yaml` separator fix untouched (verified via `git diff`
  before/after, see §1).
- **Bug found & fixed:** `charts/ojs-operator/templates/service.yaml` (the webhook Service,
  port 443→9443) rendered **unconditionally**, regardless of `webhook.enabled`. Confirmed via
  `helm template --set webhook.enabled=false` before the fix: the Service still rendered. Fixed
  by wrapping the template in `{{- if .Values.webhook.enabled -}} ... {{- end }}`, consistent
  with `webhook.yaml`'s existing guard on the same value.
- **Bug found & fixed:** `values.yaml` had a **self-contradictory comment block** directly above
  `webhook:` — one line said "Webhooks are DISABLED by default", the very next lines said
  "Enabled by default", while `webhook.enabled: true` is the actual default. Reordered/corrected
  the comment so the warning about disabling correctly follows the accurate default-enabled
  description. No values were renamed or had their defaults changed.
- Reviewed `_helpers.tpl`, `deployment.yaml`, `pdb.yaml`, `webhook.yaml`, `service.yaml`,
  `rbac.yaml`, and the CRD templates for further whitespace/separator/conditional issues; found
  none beyond the two above.
- **New test file:** `charts/ojs-operator/chart_test.go` (Go package `chart`, skipped gracefully
  via `t.Skip` if `helm` is not on `PATH`) — a rendering matrix covering: `TestHelmLint`;
  ServiceAccount/RBAC present-by-default and disabled; webhook Service +
  ValidatingWebhookConfiguration present/absent under `webhook.enabled` on/off and
  `certManager.enabled` on/off; ServiceMonitor RBAC rule toggle; PodDisruptionBudget
  enabled/disabled + `minAvailable` vs `maxUnavailable` precedence; CRD install toggle; and a
  generic guard (`TestRenderNoDoubleDashSeparatorArtifacts`) that fails if any rendered document
  separator (`---`) is found glued to following content without a newline — the exact class of
  bug the pre-existing `rbac.yaml` fix (preserved untouched) addressed.
- Follow-up chart remediation expanded this matrix with full
  `policy/poddisruptionbudgets` ClusterRole verb assertions, enabled/disabled Deployment
  `ENABLE_WEBHOOKS` assertions, TLS secret volume/mount and certificate-directory assertions,
  default self-signed issuer matching, and custom `Issuer`/`ClusterIssuer` override coverage.

### 2.8 Long-function / error / dead-code audit (item 8)

**>50-line function audit** (measured by brace-matched function bodies, excluding `_test.go`):

| Before | Lines | After |
|---|---|---|
| `OJSClusterReconciler.Reconcile` | 96 | ~35 (split into 4 helpers, see §2.1) |
| `OJSWorkerReconciler.Reconcile` | 81 | ~30 (split into 3 helpers, see §2.2) |
| `reconcileDeployment` (cluster, inline closure) | ~190 | builders extracted to `ojscluster_desired.go`, orchestration ~15 |
| `reconcileWorkerDeployment` (worker, inline closure) | ~70 | builders extracted, orchestration ~18 |
| `desiredServerContainer` | 65 | 15 (probes/resources extracted to named builders) |
| `applyRedisDeploymentSpec` | 75 | 20 (container/security extracted) |
| `Collector.Poll` | 52 | 18 (`fetchQueueStats` extracted) |
| `cmd/manager/main.go: main` | 66 | Sequential manager wiring plus explicit webhook enablement/cert-directory configuration |

`main.go` remains sequential, low-cyclomatic-complexity manager/controller/webhook wiring.
It now computes webhook enablement once and configures controller-runtime's webhook server with
the certificate directory supplied by the Helm Deployment before registering validators.

**Ignored-error / ignored-status-update audit:** every `r.Update(...)` and `r.Status().Update(...)`
call site in `internal/controller/*.go` was inventoried; all either return the error to the
caller or explicitly log it (the cluster-not-found path already had this fixed in a prior commit,
`5ffd609`, which is preserved). No ignored status updates were found remaining.

**Dead code found & removed:**
- `condScaling = "Scaling"` in the old `ojsworker_controller.go` was declared but never
  referenced anywhere — removed during the split (confirmed via
  `golangci-lint run --no-config -E staticcheck`, 0 issues after removal).
- `reconcileRequeueDelay` became dead after the requeue-behavior fix below and was removed
  along with its now-unused `"time"` import in `ojscluster_controller.go`.

**Requeue-behavior bug found & fixed (explicit controller errors / requeue behavior):**
`OJSClusterReconciler.Reconcile`'s error paths returned
`ctrl.Result{RequeueAfter: reconcileRequeueDelay}, err` (non-nil error **and** a non-zero
`RequeueAfter`). Per controller-runtime's own reconcile loop
(`sigs.k8s.io/controller-runtime@v0.20.4/pkg/internal/controller/controller.go:334-344`): *"The
result will always be ignored if the error is non-nil... the non-nil error causes requeuing with
exponential backoff"* — and it additionally logs a `Warning:` on every such occurrence. This means
the intended flat 10s requeue delay had **never** actually taken effect on any error path; it was
silently discarded every time in favor of the default rate-limited backoff, while also spamming
warning logs. Fixed by returning `ctrl.Result{}, err` on all three affected branches (embedded
backend, child resources, status update), matching the pattern the OJSWorker controller already
used correctly. Verified via `go vet`/build, the existing test suite, and a new explicit
regression assertion added to `TestReconcile_EmbeddedUnsupportedBackend` (checks
`result == ctrl.Result{}` when `err != nil`).

**HPA deletion bug found & fixed:** `OJSWorkerReconciler.reconcileWorkerHPA`'s disable branch was:
```go
if err := r.Get(ctx, ..., hpa); err == nil {
    return r.Delete(ctx, hpa)
}
return nil
```
Any error from `Get` — not just `NotFound` — was silently treated as "nothing to delete",
including transient API errors, throttling, or network blips, meaning the operator could
believe an HPA was already gone (or never take action to remove it) without ever surfacing the
problem. Fixed in `ojsworker_hpa.go` (`deleteWorkerHPA`) to explicitly distinguish
`apierrors.IsNotFound` (no-op) from any other error (now propagated to the caller so Reconcile
retries). Cleanup now additionally requires `metav1.IsControlledBy(hpa, worker)` before delete,
so ownerless or other-owned same-name HPAs are preserved, and tolerates a NotFound race on the
subsequent `Delete` via `client.IgnoreNotFound`. Regression tests cover NotFound, current-worker
ownership, ownerless and other-worker ownership, and — using controller-runtime's
`client/interceptor` package — a non-NotFound `Get` failure.

**Optional-ServiceMonitor bugs found & fixed:**
1. **Orphan resource bug:** enabling `spec.monitoring.{enabled,serviceMonitor}` created a
   ServiceMonitor, but disabling it again left the ServiceMonitor behind forever (no delete
   path existed at all) — unlike the OJSWorker HPA, which is correctly deleted when
   autoscaling is disabled. Fixed by adding `deleteServiceMonitor` and a new
   `reconcileServiceMonitorPhase` orchestrator (`ojscluster_resources.go`) that reconciles the
   ServiceMonitor when wanted and deletes it (tolerating "already gone"/CRD-not-installed via
   `apierrors.IsNotFound`) when not, mirroring the HPA fix's error-handling shape. Wired into
   `Reconcile` in place of the old inline `if` block.
2. **Crash bug (found while adding regression tests for #1):** `desiredServiceMonitorSpec`
   placed a raw `map[string]string` (`matchLabels: labels`) directly into
   `unstructured.Unstructured` object content. `unstructured` content is required to be built
   only from plain JSON-compatible types (`map[string]interface{}`, `[]interface{}`, `string`,
   numbers, `bool`, `nil`); a `map[string]string` panics
   (`"cannot deep copy map[string]string"`) the first time anything calls
   `.DeepCopyObject()` on it — which happens in the fake client's object tracker in tests, and
   in controller-runtime's cache/patch machinery against a real cluster. This code path had
   **zero prior test coverage**, so the bug had never been observed. Fixed by adding
   `stringMapToInterfaceMap` and using it for `matchLabels`. New tests:
   `TestReconcileServiceMonitorPhase_CreatesWhenEnabled` (would have panicked before the fix),
   `TestReconcileServiceMonitorPhase_RemovesWhenDisabled` (regression for #1),
   `TestReconcileServiceMonitorPhase_NoOpWhenNeverEnabled`, plus an updated
   `TestDesiredServiceMonitorSpec` asserting the corrected type.

**Metrics transport:** covered under §2.5 (deep copies, stale eviction, `Poll` decomposition,
`resp.Body.Close()` error handling). No other transport-layer bugs were found — request
construction, auth header handling, and status-code/error-body handling were already correct.

### 2.9 Build reproducibility fix (not in the original list, blocking all gates)

`go.sum` was missing entries for several transitive dependencies' `go.mod` files (an
environment/toolchain mismatch pre-existing on this branch, unrelated to any application code),
which made `GOFLAGS=-mod=readonly go build ./...` fail immediately with
`"missing go.sum entry for go.mod file"` errors before any refactor work could even compile.
Ran `go mod tidy` (module cache already had the required packages available; no `go.mod`
requirement changes resulted, confirmed via `git diff go.mod` showing no diff) to add the missing
`go.sum` entries. This was necessary for every one of the "final gates" below to run at all under
`GOFLAGS=-mod=readonly`.

### 2.10 Review findings remediation

Three follow-up review findings were fixed without changing the OJSCluster API:

1. **Service updates preserve API-owned/defaulted state.** The server and embedded Redis
   reconcilers no longer replace the complete `ServiceSpec`. They project only the operator-owned
   selector and port definitions, and only apply a Service type when a desired type is explicitly
   present. Existing node-port assignments are merged by port name/protocol. Consequently,
   `ClusterIP`, `ClusterIPs`, `IPFamilies`, `IPFamilyPolicy`, `HealthCheckNodePort`,
   traffic-policy/defaulting fields, load-balancer fields, and other API-managed state survive
   later reconciles. `ojscluster_service_test.go` uses fake-client update interceptors on a
   second reconcile to reject any attempted clearing while also proving stale selectors/ports
   are updated for both Services.
2. **Disabled ServiceMonitor cleanup is ownership-safe.** `deleteServiceMonitor` now deletes
   only when `metav1.IsControlledBy` confirms that the same-name resource is controlled by the
   current OJSCluster. Explicit owner-reference tests cover current-owner deletion plus unowned
   and sibling-owned collisions remaining untouched.
3. **A missing ServiceMonitor GVK is an expected optional-capability absence.** Both
   create/update and cleanup paths treat `meta.IsNoMatchError` as success, including a CRD
   disappearing between lookup and deletion. Fake-client interceptor tests simulate
   `NoKindMatchError` for enabled reconciliation and disabled cleanup and assert that neither an
   error nor a warning event is produced.

### 2.11 Follow-up findings remediation

1. **Helm PDB RBAC:** the ClusterRole now grants `create`, `delete`, `get`, `list`, `patch`,
   `update`, and `watch` on `policy/poddisruptionbudgets`, matching the controller's
   `CreateOrUpdate`, cleanup, and watch behavior. The chart test asserts the complete rule.
2. **Webhook startup/TLS wiring:** the Deployment always sets `ENABLE_WEBHOOKS` from
   `webhook.enabled`. Enabled renders the Service and validating configuration, exposes port
   9443, mounts `<release-fullname>-webhook-server-cert` read-only at
   `/tmp/k8s-webhook-server/serving-certs`, and passes that same directory through
   `WEBHOOK_CERT_DIR`; `cmd/manager` configures controller-runtime's webhook server from it.
   Disabled renders none of the webhook resources or TLS volume/mount and sets the env value to
   `"false"`. Certificate/Issuer rendering remains conditional on cert-manager. An empty issuer
   name computes and references the rendered `<release-fullname>-selfsigned-issuer`; a custom
   issuer name/kind is preserved and suppresses the unused generated Issuer. With cert-manager
   disabled, users must provision the expected TLS Secret externally.
3. **HPA replica ownership:** new autoscaled worker Deployments initialize from
   `spec.replicas`, bounded by `minReplicas`/`maxReplicas` (or from `minReplicas` when replicas
   is omitted). Once the Deployment exists and autoscaling is enabled, reconciles preserve the
   HPA-written replica count while continuing to reconcile all other fields. Disabling
   autoscaling resumes replica management from `spec.replicas`. Pure builder and fake-client
   lifecycle tests cover create, update, and enable/disable behavior.
4. **PDB cleanup:** when replicas drop to one or the OJSCluster PDB is disabled,
   `reconcilePDB` now deletes an existing same-name PDB only if
   `metav1.IsControlledBy` confirms current-cluster ownership. Unowned collisions are preserved;
   NotFound and NoMatch from lookup or deletion are no-ops. Focused tests cover single-replica
   cleanup, explicit disable, unowned preservation, missing objects, and missing API matches.
5. **HPA cleanup ownership:** disabling worker autoscaling now passes the current
   `OJSWorker` identity into cleanup and deletes the same-name HPA only when
   `metav1.IsControlledBy` confirms that worker is the controller owner. Ownerless and
   other-worker-owned collisions remain untouched; NotFound remains a no-op and non-NotFound
   lookup failures remain retryable errors. Tests cover owned, ownerless, other-owned,
   not-found, and lookup-error cases.
6. **PDB non-negative availability counts:** `PDBSpec.MinAvailable` and `MaxUnavailable` now
   carry kubebuilder minimum-zero markers; Go admission validation rejects negative values even
   when the PDB is otherwise optional, while explicit zero remains valid. Desired-state builder
   tests prove zero is projected as an explicit `intstr.IntOrString` rather than falling through
   to the default.

### 2.12 Manager and manifest parity remediation

1. **Leader-election flag handling:** `cmd/manager` now parses the standard
   `--leader-elect` flag together with controller-runtime zap flags before manager creation.
   Leader election defaults to false in the binary and is copied into
   `ctrl.Options.LeaderElection`; Helm preserves its existing default of true by rendering the
   argument, while deployment manifests opt in explicitly. Unit tests cover defaults, explicit
   true/false values, zap flags, invalid arguments, and manager-option construction without
   starting a cluster.
2. **Lease and PDB RBAC parity:** Helm renders the complete
   `coordination.k8s.io/leases` rule only while `leaderElection.enabled=true`. The raw
   ClusterRole includes the same Lease rule because its Deployment passes `--leader-elect`, and
   now also includes the controller-required `policy/poddisruptionbudgets` rule already present
   in Helm.
3. **Safe raw webhook default:** the raw manager Deployment keeps `--leader-elect` but sets
   `ENABLE_WEBHOOKS=false` and has no webhook certificate environment, volume, or mount. Helm
   continues to enable webhooks and provision/mount certificates by default. README and operator
   docs explain that raw users must provide TLS, admission resources, the certificate directory,
   and explicitly enable webhooks.
4. **Parsed manifest tests:** chart tests parse Helm-rendered and raw Deployment/ClusterRole YAML
   into Kubernetes API types. They assert the leader-election argument and conditional Lease
   matrix, raw-versus-Helm webhook environment/TLS behavior, and exact Lease/PDB verb parity.

### 2.13 OJSCluster CRD schema synchronization

The Go API already exposed `spec.podDisruptionBudget`, but both the installable config CRD and
the Helm-rendered CRD omitted it. Kubernetes API-server pruning would therefore discard
`enabled`, `minAvailable`, and `maxUnavailable` before the controller could observe them.

Both CRDs now define the existing `PDBSpec` shape in the same location and order: optional
`enabled` (`boolean`), `minAvailable` (`integer`, `int32`, `minimum: 0`), and `maxUnavailable`
(`integer`, `int32`, `minimum: 0`) fields with descriptions matching
`api/v1alpha1/ojscluster_types.go`. No fields were made required, preserving the
pointer/`omitempty` distinction between absent values and explicit `false`/zero values.

`config/crd/ojscluster_crd_test.go` parses the config CRD, asserts the object and field schemas,
rejects negative values through the parsed OpenAPI minimum constraints, validates a sample
OJSCluster containing explicit `false`/zero PDB values, and runs Kubernetes structural pruning
to prove those values are retained. The Helm chart test performs the same checks against the
rendered CRD.

### 2.14 ServiceMonitor retry semantics

Transient ServiceMonitor API failures are no longer swallowed after their warning log/event.
`reconcileServiceMonitorPhase` now returns non-NoMatch create/update/get/delete errors to
`Reconcile`, which returns a zero `ctrl.Result` plus the error so controller-runtime performs its
normal rate-limited retry. `apierrors.IsNotFound` remains a successful obsolete-cleanup outcome,
and `meta.IsNoMatchError` remains successful optional-capability absence for both desired
reconciliation and cleanup.

Status refresh intentionally remains after the ServiceMonitor phase: even when the optional
resource call fails, `updateStatus` still observes and persists Deployment state before
`Reconcile` returns the retry-triggering error. Warning logging/event emission remains solely in
`reconcileServiceMonitorPhase`, exactly once per failed reconcile attempt.

`ojscluster_servicemonitor_test.go` now uses fake-client interceptors to fail the first
ServiceMonitor create, update, delete, and obsolete-cleanup get call. Each case asserts a
non-nil top-level reconcile error with a zero result, persisted status refresh, exactly one
warning event, successful next reconcile, and the final desired ServiceMonitor state. Additional
top-level enabled/cleanup tests inject `NoKindMatchError` and assert success with no event.

---

## 3. File Evidence

**Modified:**
- `README.md` / `docs/configuration.md` / `docs/getting-started.md` /
  `docs/troubleshooting.md` — documented HPA/PDB behavior plus Helm-versus-raw webhook
  provisioning and opt-in requirements.
- `charts/ojs-operator/templates/_helpers.tpl` — shared webhook secret, certificate directory,
  and computed issuer helpers.
- `charts/ojs-operator/templates/deployment.yaml` — webhook env, port, TLS volume, and mount.
- `charts/ojs-operator/templates/webhook.yaml` — computed default/custom issuer behavior and
  shared generated-secret name.
- `cmd/manager/main.go` — standard leader-election/zap flag parsing, manager-option
  construction, and controller-runtime webhook server cert-directory configuration.
- `charts/ojs-operator/templates/rbac.yaml` — preserved pre-existing separator fix plus
  conditional Lease and PDB policy permissions.
- `config/manager/deployment.yaml` — explicit leader-election opt-in and safe
  `ENABLE_WEBHOOKS=false` raw default.
- `config/rbac/role.yaml` — raw Lease and PDB permissions matching enabled Helm behavior.
- `charts/ojs-operator/templates/service.yaml` — added `webhook.enabled` guard.
- `charts/ojs-operator/templates/crds/ojscluster-crd.yaml` — synchronized the existing
  `spec.podDisruptionBudget` API schema and its minimum-zero constraints.
- `config/crd/ojscluster-crd.yaml` — synchronized the same installable
  `spec.podDisruptionBudget` API schema and constraints.
- `api/v1alpha1/ojscluster_types.go` — added kubebuilder minimum-zero markers to the optional
  PDB availability counts.
- `charts/ojs-operator/values.yaml` — corrected webhook comments and documented
  certificate/PDB/leader-election RBAC behavior without renaming values.
- `go.sum` — added missing transitive `go.mod` checksums (see §2.9); `go.mod` unchanged.
- `internal/controller/ojscluster_controller.go` — lifecycle + registration, including
  status-preserving propagation of ServiceMonitor retry errors.
- `internal/controller/ojsworker_controller.go` — reduced to lifecycle + registration.
- `internal/controller/ojscluster_controller_extended_test.go` — added zero-`Result`-on-error
  regression assertion.
- `internal/controller/status_test.go` — gofmt whitespace only (trailing blank line).
- `internal/metrics/queue_metrics.go` — deep copies, stale eviction, `Poll` split.
- `internal/metrics/queue_metrics_test.go` — added race/alias/staleness tests, gofmt only
  otherwise.
- `internal/webhook/ojscluster_webhook.go` — immutable internal validation set plus PDB
  non-negative validation.
- `internal/webhook/webhook_test.go` — added mutation-has-no-effect, concurrency, and PDB
  negative/zero validation tests.

**New:**
- `charts/ojs-operator/chart_test.go` — Helm rendering matrix plus parsed raw/Helm manifest
  parity tests.
- `cmd/manager/main_test.go` — leader-election/zap parsing and manager-option unit tests.
- `internal/controller/ojscluster_desired.go` / `ojscluster_desired_test.go`
- `internal/controller/ojscluster_resources.go` — child-resource reconciliation,
  ownership-safe obsolete-resource cleanup, and retryable ServiceMonitor phase errors.
- `internal/controller/ojscluster_pdb_test.go` — obsolete-PDB cleanup and ownership tests.
- `internal/controller/ojscluster_service_test.go` — second-reconcile Service preservation tests.
- `internal/controller/ojscluster_status.go`
- `internal/controller/ojscluster_servicemonitor_test.go` — lifecycle, ownership, missing-GVK,
  transient API failure, top-level retry, status, and warning-event tests.
- `internal/controller/ojsworker_deployment.go`
- `internal/controller/ojsworker_hpa.go` / `ojsworker_hpa_test.go` — HPA reconciliation plus
  current-worker ownership-safe disabled-autoscaling cleanup tests.
- `internal/controller/ojsworker_status.go`
- `internal/controller/ojsworker_desired.go` / `ojsworker_desired_test.go` —
  autoscaling-aware Deployment replica ownership and lifecycle tests.
- `internal/controller/status.go` — shared condition/status transition policy.
- `AUDIT.md` (this file).
- `config/crd/ojscluster_crd_test.go` — config CRD schema, validation, and pruning regression
  coverage.

**Untouched (verified):** `api/v1alpha1/zz_generated_deepcopy.go`, all CRD schema content
outside the targeted OJSCluster `spec.podDisruptionBudget` additions/constraints, and
`internal/webhook/ojsworker_webhook.go`.

---

## 4. Verification Commands & Results

All commands ran from the `ojs-k8s-operator` repository root (standalone module).

```
$ GOWORK=off GOFLAGS=-mod=readonly go test ./... -race -cover -count=1
ok   github.com/openjobspec/ojs-k8s-operator/charts/ojs-operator
ok   github.com/openjobspec/ojs-k8s-operator/cmd/manager       32.7% of statements
ok   github.com/openjobspec/ojs-k8s-operator/config/crd
ok   github.com/openjobspec/ojs-k8s-operator/internal/controller  87.3% of statements
ok   github.com/openjobspec/ojs-k8s-operator/internal/metrics     92.6% of statements
ok   github.com/openjobspec/ojs-k8s-operator/internal/webhook     100.0% of statements

$ GOWORK=off GOFLAGS=-mod=readonly go vet ./...
(no output — clean)

$ GOWORK=off GOFLAGS=-mod=readonly go build -o /tmp/ojs-k8s-operator-manager ./cmd/manager
(succeeds)

$ helm lint charts/ojs-operator
1 chart(s) linted, 0 chart(s) failed

$ helm template test charts/ojs-operator --values charts/ojs-operator/values.yaml
(succeeds)

$ docker build -t ojs-k8s-operator:audit-test .
(succeeds — multi-stage Linux build to gcr.io/distroless/static:nonroot)

$ git diff --check
(no output — clean, no whitespace errors)

$ diff -u config/crd/ojscluster-crd.yaml \
    <(sed '1d;/^{{- end }}$/,$d' charts/ojs-operator/templates/crds/ojscluster-crd.yaml)
(no output — config and chart OJSCluster CRDs are synchronized)

$ find . -type f -name '*.go' -not -path './.git/*' -exec gofmt -l {} +
(no output — all Go files formatted)
```

The manager/RBAC/raw-manifest follow-up reran the uncached race/coverage suite,
`go vet ./...`, manager build, Helm lint/template, formatting, and
`git diff --check`; all passed.

The ServiceMonitor retry follow-up reran every gate above with
`GOWORK=off GOFLAGS=-mod=readonly`, including the uncached full race/coverage
suite, vet, manager build, Helm lint/template, Docker build, CRD parity,
repository-wide Go formatting, and `git diff --check`; all passed. The
ServiceMonitor-focused controller suite also passed independently before the
full run.

The full race run includes config and rendered-chart CRD minimum/validation/pruning tests, the
PDB/Lease RBAC and webhook/leader-election Helm matrix, parsed raw/Helm parity tests,
manager flag-option tests, autoscaled Deployment create/update/toggle tests, ownership-safe PDB
cleanup tests, current-worker ownership-safe HPA cleanup tests, and transient
ServiceMonitor create/update/delete/get retry tests with NoMatch success. The
final default Helm render SHA-256 is
`9d074f4ac3a03cd678a703713ec9a314b9c0d01eba11efb66a9c62006e003450`.

Also run for extra confidence (not in the required gate list, but useful signal):
`golangci-lint run --no-config --tests=false -E staticcheck,unused,errcheck ./...` → 0 issues
(golangci-lint v2.12.2 is installed, but the repo's `.golangci.yml` is v1-format and is rejected
by v2 with `"unsupported version of the configuration"`; `make lint` uses `go vet`, which is
unaffected — see §5 unresolved items).

---

## 5. Assumptions

- **"Cohesive files" granularity**: interpreted item 1/2 as splitting into lifecycle/
  registration, status, resource-reconciliation, and pure-builder files per kind (4 files for
  OJSCluster, 5 for OJSWorker including the HPA/Deployment split called out explicitly in item
  2), rather than one file per individual method.
- **Shared status policy parameterization**: since cluster and worker condition *messages*
  differ in wording (not just a single substitutable noun — see §2.4), the shared function takes
  fully-formed message strings per kind rather than a single templated noun, to guarantee
  byte-identical preservation of historical text.
- **`ValidBackendTypes` decoupling is a documented behavior change**, not a silent one: the
  exported map's doc comment states plainly that mutating it no longer affects validation.
- **Docker/Helm availability**: assumed available in this environment (both were present and
  used for direct verification) rather than skipped.
- **`go.sum` fix**: treated as in-scope/necessary because it blocked every requested gate under
  `GOFLAGS=-mod=readonly`; limited to adding missing checksums via `go mod tidy` (verified
  `go.mod` requirements unchanged).
- **`reconcileServiceMonitorPhase` and HPA-deletion fixes** are treated as within item 8's
  "optional ServiceMonitor" / "HPA deletion" callouts specifically because intended behavior is
  unambiguous: (a) an optional, disable-able child resource should not be orphaned (directly
  analogous to the HPA precedent already in the same codebase), and (b) a non-NotFound error
  must never be silently treated as "already deleted." The ServiceMonitor `unstructured` type
  panic is fixed because it is a correctness bug (not a design choice) with a single unambiguous
  fix (unstructured content must be JSON-compatible types).
- **`main.go` and `applyRedisDeploymentSpec`'s embedded-backend scope**: no changes to embedded
  backend *support* (still redis-only) — that is a feature-scope decision, not a bug, and was
  left alone per the instruction to fix bugs "where intended behavior is clear," not to expand
  scope.
- **Autoscaled worker initialization:** interpreted "from spec replicas/min" as respecting an
  explicit `spec.replicas` within the configured HPA range, raising it to `minReplicas` when
  lower or omitted, and capping it at `maxReplicas` when higher.
- **Webhooks without cert-manager:** interpreted `webhook.certManager.enabled=false` as using an
  externally provisioned Secret with the same chart-defined name. Service/validation and the
  Deployment mount remain enabled, while Certificate/Issuer resources are omitted.

## 6. Unresolved / Follow-up Items

- **`.golangci.yml` is v1-format**; the installed `golangci-lint` (v2.12.2) refuses to load it
  (`"unsupported version of the configuration"`). This does not block any requested gate
  (`make lint` = `go vet ./...`, which passes cleanly), but the config should be migrated to v2
  format (`golangci-lint migrate`) as a follow-up so richer linting (`gosec`, `gocritic`,
  `gocyclo`, etc., all already listed in the existing config) actually runs in CI.
- **Embedded backend support remains redis-only** (`reconcileEmbeddedBackend` still rejects
  `postgres`/`nats`/`kafka`/`sqs` embedded requests with an explicit error) — unchanged, since
  expanding it is a feature addition, not a bug fix, and out of this audit's scope.

# Troubleshooting

Common issues and solutions when using the OJS Kubernetes Operator.

## Operator Issues

### Operator pod is in CrashLoopBackOff

**Check logs:**

```bash
kubectl logs -n ojs-system -l control-plane=controller-manager --tail=50
```

**Common causes:**

1. **CRDs not installed** — Install CRDs before deploying the operator:
   ```bash
   kubectl apply -f config/crd/
   ```

2. **RBAC permissions missing** — Ensure the ClusterRole and ClusterRoleBinding are created:
   ```bash
   kubectl apply -f config/rbac/
   ```

3. **Leader election failure** — If running multiple replicas, ensure the operator has permissions to create/update `Lease` objects.

### Operator is running but not reconciling

**Check if the operator is the leader:**

```bash
kubectl get lease -n ojs-system ojs-k8s-operator.openjobspec.dev
```

**Check operator logs for errors:**

```bash
kubectl logs -n ojs-system -l control-plane=controller-manager -f
```

## OJSCluster Issues

### Cluster stuck in "Pending" phase

This means the server Deployment exists but no pods are ready.

**Check Deployment status:**

```bash
kubectl get deployment <cluster-name>-server
kubectl describe deployment <cluster-name>-server
```

**Check pod status:**

```bash
kubectl get pods -l app.kubernetes.io/instance=<cluster-name>,app.kubernetes.io/component=server
kubectl describe pod <pod-name>
```

**Common causes:**

1. **Image pull errors** — Verify the image exists and pull secrets are configured
2. **Resource quota exceeded** — Check namespace resource quotas
3. **Backend unreachable** — The server pod may be crash-looping if it cannot connect to the backend

### Cluster in "Scaling" phase

This is normal during scale-up/down. If it persists:

```bash
kubectl get deployment <cluster-name>-server
```

Check if pods are stuck in `Pending` (scheduling issues) or `CrashLoopBackOff` (application issues).

### Backend connection failures

**Check ConfigMap for correct URL:**

```bash
kubectl get configmap <cluster-name>-config -o yaml
```

**For embedded Redis, check Redis pod:**

```bash
kubectl get pods -l app.kubernetes.io/instance=<cluster-name>,app.kubernetes.io/component=backend
kubectl logs <redis-pod-name>
```

**For secret-referenced URLs:**

```bash
kubectl get secret <secret-name> -o jsonpath='{.data.<key>}' | base64 -d
```

### Embedded Redis not supported error

Only `redis` supports embedded mode. Other backend types (`postgres`, `nats`, `kafka`, `sqs`, `lite`) require an external instance.

## OJSWorker Issues

### Worker in "Error" phase with "ClusterNotFound"

The worker references an OJSCluster that doesn't exist.

**Verify the cluster exists:**

```bash
kubectl get ojsclusters
```

**Check the worker's clusterRef:**

```bash
kubectl get ojsworker <worker-name> -o jsonpath='{.spec.clusterRef}'
```

The worker will automatically retry every 30 seconds. Once the cluster is created, the worker will recover.

### Worker pods not starting

**Check events:**

```bash
kubectl describe ojsworker <worker-name>
kubectl get events --field-selector involvedObject.name=<worker-name>
```

**Check the worker Deployment:**

```bash
kubectl get deployment <worker-name>
kubectl describe deployment <worker-name>
```

**Verify the worker image exists and is correct:**

```bash
kubectl get ojsworker <worker-name> -o jsonpath='{.spec.image}'
```

### HPA not scaling workers

**Check HPA status:**

```bash
kubectl get hpa <worker-name>-hpa
kubectl describe hpa <worker-name>-hpa
```

**Common causes:**

1. **Metrics server not installed** — HPA requires the metrics-server for CPU-based scaling:
   ```bash
   kubectl get deployment metrics-server -n kube-system
   ```

2. **No resource requests set** — CPU-based HPA requires resource requests on worker containers:
   ```yaml
   spec:
     resources:
       requests:
         cpu: 100m
   ```

3. **Autoscaling disabled** — Verify `autoScaling.enabled: true` in the worker spec.

### HPA still exists after disabling auto-scaling

The operator deletes the HPA when `autoScaling.enabled` is set to `false`. If it persists, trigger a reconciliation:

```bash
kubectl annotate ojsworker <worker-name> reconcile=$(date +%s)
```

## Webhook Issues

### Admission webhook errors on create/update

**Check if webhooks are running:**

```bash
kubectl get validatingwebhookconfigurations
```

**Disable webhooks (development only):**

Set `ENABLE_WEBHOOKS=false` on the operator deployment.

### "connection refused" errors from webhooks

Webhook endpoints require TLS certificates. Ensure cert-manager or your certificate solution is properly configured.

## Status Conditions

### Understanding conditions

```bash
kubectl get ojscluster <name> -o jsonpath='{.status.conditions}' | jq .
```

| Condition | True | False |
|-----------|------|-------|
| `Ready` | All replicas ready | Some or no replicas ready |
| `Available` | At least one replica ready | No replicas available |
| `Progressing` | Rollout or scaling in progress | Deployment stable |
| `Degraded` | Fewer replicas than desired | All replicas healthy |

### Stale conditions (observedGeneration mismatch)

If a condition's `observedGeneration` is less than the resource's `metadata.generation`, the condition hasn't been updated for the latest spec change. This is normal during reconciliation — wait a few seconds and check again.

## Monitoring Issues

### ServiceMonitor not created

**Verify monitoring is enabled:**

```yaml
spec:
  monitoring:
    enabled: true
    serviceMonitor: true
```

**Check if prometheus-operator CRDs are installed:**

```bash
kubectl get crd servicemonitors.monitoring.coreos.com
```

**Check operator logs for ServiceMonitor errors:**

```bash
kubectl logs -n ojs-system -l control-plane=controller-manager | grep -i servicemonitor
```

## Getting Help

1. Check operator logs: `kubectl logs -n ojs-system -l control-plane=controller-manager`
2. Check events: `kubectl get events --sort-by=.metadata.creationTimestamp`
3. Describe the resource: `kubectl describe ojscluster <name>` or `kubectl describe ojsworker <name>`
4. File an issue: [github.com/openjobspec/ojs-k8s-operator/issues](https://github.com/openjobspec/ojs-k8s-operator/issues)

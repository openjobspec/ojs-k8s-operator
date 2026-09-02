package controller

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// This file contains the pure desired-state policy for OJSWorker child
// resources (the Deployment projection and the HPA policy), plus the
// label/serialization helpers they share. Like ojscluster_desired.go, these
// functions perform no I/O and hold no controller state, so they can be
// unit/characterization tested directly.

// workerReplicas returns the effective replica count for the worker
// Deployment, applying the documented default of 1 when unset.
func workerReplicas(worker *ojsv1alpha1.OJSWorker) int32 {
	if worker.Spec.Replicas != nil {
		return *worker.Spec.Replicas
	}
	return 1
}

func workerAutoScalingEnabled(worker *ojsv1alpha1.OJSWorker) bool {
	return worker.Spec.AutoScaling != nil && worker.Spec.AutoScaling.Enabled
}

// initialWorkerReplicas chooses a valid starting size for a new
// autoscaled Deployment. Explicit replicas are respected within the HPA
// bounds; when replicas are omitted or below the minimum, minReplicas wins.
func initialWorkerReplicas(worker *ojsv1alpha1.OJSWorker) int32 {
	replicas := workerReplicas(worker)
	if !workerAutoScalingEnabled(worker) {
		return replicas
	}
	if replicas < worker.Spec.AutoScaling.MinReplicas {
		replicas = worker.Spec.AutoScaling.MinReplicas
	}
	if worker.Spec.AutoScaling.MaxReplicas > 0 && replicas > worker.Spec.AutoScaling.MaxReplicas {
		replicas = worker.Spec.AutoScaling.MaxReplicas
	}
	return replicas
}

// workerTerminationGracePeriod returns the effective termination grace
// period in seconds, applying the default of 30s when GracefulShutdown is
// unset or non-positive.
func workerTerminationGracePeriod(worker *ojsv1alpha1.OJSWorker) int64 {
	if worker.Spec.GracefulShutdown != nil && worker.Spec.GracefulShutdown.TimeoutSeconds > 0 {
		return int64(worker.Spec.GracefulShutdown.TimeoutSeconds)
	}
	return 30
}

func labelsForWorker(workerName, clusterName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "ojs-worker",
		"app.kubernetes.io/instance":   workerName,
		"app.kubernetes.io/component":  "worker",
		"app.kubernetes.io/part-of":    "ojs",
		"app.kubernetes.io/managed-by": "ojs-k8s-operator",
		"ojs.openjobspec.dev/cluster":  clusterName,
	}
}

// joinStrings serializes a slice of strings as a comma-separated value,
// matching the format expected by the OJS worker's env-var based
// configuration (OJS_QUEUES, OJS_JOB_TYPES).
func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ","
		}
		result += s
	}
	return result
}

// desiredWorkerEnvVars computes the environment variables for the
// ojs-worker container.
func desiredWorkerEnvVars(worker *ojsv1alpha1.OJSWorker, cluster *ojsv1alpha1.OJSCluster) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
		{Name: "OJS_URL", Value: fmt.Sprintf("http://%s-server.%s.svc.cluster.local:%d",
			cluster.Name, cluster.Namespace, defaultPort)},
		{Name: "OJS_QUEUES", Value: joinStrings(worker.Spec.Queues)},
		{Name: "OJS_JOB_TYPES", Value: joinStrings(worker.Spec.JobTypes)},
	}
	if worker.Spec.Concurrency > 0 {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "OJS_CONCURRENCY",
			Value: fmt.Sprintf("%d", worker.Spec.Concurrency),
		})
	}
	envVars = append(envVars, worker.Spec.Env...)
	return envVars
}

// desiredWorkerContainer computes the ojs-worker container spec.
func desiredWorkerContainer(worker *ojsv1alpha1.OJSWorker, cluster *ojsv1alpha1.OJSCluster) corev1.Container {
	container := corev1.Container{
		Name:  "ojs-worker",
		Image: worker.Spec.Image,
		Env:   desiredWorkerEnvVars(worker, cluster),
	}

	if len(worker.Spec.Command) > 0 {
		container.Command = worker.Spec.Command
	}

	if worker.Spec.Resources.Limits != nil || worker.Spec.Resources.Requests != nil {
		container.Resources = worker.Spec.Resources
	}

	return container
}

// applyWorkerDeploymentSpec projects the desired worker Deployment fields
// (labels, replicas, selector, pod template) onto dep. It mutates dep in
// place so it can be used directly inside a controllerutil.CreateOrUpdate
// mutate callback.
func applyWorkerDeploymentSpec(dep *appsv1.Deployment, worker *ojsv1alpha1.OJSWorker, cluster *ojsv1alpha1.OJSCluster) {
	labels := labelsForWorker(worker.Name, cluster.Name)

	dep.Labels = labels
	if dep.ResourceVersion == "" || !workerAutoScalingEnabled(worker) {
		replicas := initialWorkerReplicas(worker)
		dep.Spec.Replicas = &replicas
	}
	dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}

	container := desiredWorkerContainer(worker, cluster)
	terminationGracePeriod := workerTerminationGracePeriod(worker)

	dep.Spec.Template = corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec: corev1.PodSpec{
			Containers:                    []corev1.Container{container},
			TerminationGracePeriodSeconds: &terminationGracePeriod,
		},
	}
}

func int32PtrVal(i int32) *int32 { return &i }

// desiredHPASpec computes the HorizontalPodAutoscaler spec for a worker,
// targeting the worker's own Deployment and scaling on CPU utilization.
func desiredHPASpec(worker *ojsv1alpha1.OJSWorker) autoscalingv2.HorizontalPodAutoscalerSpec {
	return autoscalingv2.HorizontalPodAutoscalerSpec{
		ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Name:       worker.Name,
		},
		MinReplicas: &worker.Spec.AutoScaling.MinReplicas,
		MaxReplicas: worker.Spec.AutoScaling.MaxReplicas,
		Metrics: []autoscalingv2.MetricSpec{
			{
				Type: autoscalingv2.ResourceMetricSourceType,
				Resource: &autoscalingv2.ResourceMetricSource{
					Name: corev1.ResourceCPU,
					Target: autoscalingv2.MetricTarget{
						Type:               autoscalingv2.UtilizationMetricType,
						AverageUtilization: int32PtrVal(80),
					},
				},
			},
		},
	}
}

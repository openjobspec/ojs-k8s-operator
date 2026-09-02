// Package chart contains black-box rendering/lint tests for the ojs-operator
// Helm chart. These shell out to the `helm` CLI, so they are skipped (not
// failed) when helm is not available on PATH -- e.g. in minimal Go-only CI
// environments -- to keep `go test ./...` runnable everywhere.
package chart

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"sigs.k8s.io/yaml"
)

func requireHelm(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not found on PATH; skipping chart rendering test")
	}
}

// renderChart runs `helm template` against this chart with the given --set
// overrides and returns the combined rendered output.
func renderChart(t *testing.T, sets ...string) string {
	t.Helper()
	args := []string{"template", "chart-test", "."}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	cmd := exec.Command("helm", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	return string(out)
}

// docsByKindName splits rendered multi-document YAML output into
// "kind/name" -> document body, keyed on the first "kind:" and "name:" lines
// found in each `---`-separated document. This is intentionally simple
// (string-based, not a full YAML parse) since these tests only need to
// assert presence/absence and gross structural properties.
func docsByKindName(rendered string) map[string]string {
	docs := map[string]string{}
	for _, doc := range strings.Split(rendered, "\n---\n") {
		var kind, name string
		for _, line := range strings.Split(doc, "\n") {
			trimmed := strings.TrimSpace(line)
			if kind == "" && strings.HasPrefix(trimmed, "kind:") {
				kind = strings.TrimSpace(strings.TrimPrefix(trimmed, "kind:"))
			}
			if name == "" && strings.HasPrefix(trimmed, "name:") {
				name = strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			}
		}
		if kind != "" {
			docs[kind+"/"+name] = doc
		}
	}
	return docs
}

func parseDeployment(t *testing.T, manifest string) appsv1.Deployment {
	t.Helper()
	var deployment appsv1.Deployment
	if err := yaml.Unmarshal([]byte(manifest), &deployment); err != nil {
		t.Fatalf("parse Deployment YAML: %v", err)
	}
	if deployment.Kind != "Deployment" {
		t.Fatalf("parsed kind = %q, want Deployment", deployment.Kind)
	}
	return deployment
}

func parseClusterRole(t *testing.T, manifest string) rbacv1.ClusterRole {
	t.Helper()
	var role rbacv1.ClusterRole
	if err := yaml.Unmarshal([]byte(manifest), &role); err != nil {
		t.Fatalf("parse ClusterRole YAML: %v", err)
	}
	if role.Kind != "ClusterRole" {
		t.Fatalf("parsed kind = %q, want ClusterRole", role.Kind)
	}
	return role
}

func readManifest(t *testing.T, path string) string {
	t.Helper()
	manifest, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(manifest)
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func envValue(deployment appsv1.Deployment, name string) (string, bool) {
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name != "manager" {
			continue
		}
		for _, env := range container.Env {
			if env.Name == name {
				return env.Value, true
			}
		}
	}
	return "", false
}

func ruleVerbs(role rbacv1.ClusterRole, apiGroup, resource string) ([]string, bool) {
	for _, rule := range role.Rules {
		if hasString(rule.APIGroups, apiGroup) && hasString(rule.Resources, resource) {
			verbs := append([]string(nil), rule.Verbs...)
			sort.Strings(verbs)
			return verbs, true
		}
	}
	return nil, false
}

func assertRuleVerbs(t *testing.T, role rbacv1.ClusterRole, apiGroup, resource string, want []string) {
	t.Helper()
	got, ok := ruleVerbs(role, apiGroup, resource)
	if !ok {
		t.Fatalf("missing %s/%s rule", apiGroup, resource)
	}
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s/%s verbs = %v, want %v", apiGroup, resource, got, want)
	}
}

func TestHelmLint(t *testing.T) {
	requireHelm(t)
	cmd := exec.Command("helm", "lint", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm lint failed: %v\n%s", err, out)
	}
}

func TestRenderLeaderElectionMatrix(t *testing.T) {
	requireHelm(t)

	tests := []struct {
		name          string
		sets          []string
		wantArg       bool
		wantLeaseRule bool
	}{
		{name: "enabled by default", wantArg: true, wantLeaseRule: true},
		{
			name:          "disabled explicitly",
			sets:          []string{"leaderElection.enabled=false"},
			wantArg:       false,
			wantLeaseRule: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs := docsByKindName(renderChart(t, tt.sets...))
			deployment := parseDeployment(t, docs["Deployment/chart-test-ojs-operator"])
			role := parseClusterRole(t, docs["ClusterRole/chart-test-ojs-operator-manager-role"])
			args := deployment.Spec.Template.Spec.Containers[0].Args

			if got := hasString(args, "--leader-elect"); got != tt.wantArg {
				t.Errorf("--leader-elect present = %t, want %t; args = %v", got, tt.wantArg, args)
			}
			_, hasLeaseRule := ruleVerbs(role, "coordination.k8s.io", "leases")
			if hasLeaseRule != tt.wantLeaseRule {
				t.Errorf("leases rule present = %t, want %t; rules = %#v", hasLeaseRule, tt.wantLeaseRule, role.Rules)
			}
			if tt.wantLeaseRule {
				assertRuleVerbs(t, role, "coordination.k8s.io", "leases",
					[]string{"get", "list", "watch", "create", "update", "patch", "delete"})
			}
		})
	}
}

func TestRawAndHelmManifestParity(t *testing.T) {
	requireHelm(t)

	rawDeployment := parseDeployment(t, readManifest(t, "../../config/manager/deployment.yaml"))
	rawRole := parseClusterRole(t, readManifest(t, "../../config/rbac/role.yaml"))

	docs := docsByKindName(renderChart(t))
	helmDeployment := parseDeployment(t, docs["Deployment/chart-test-ojs-operator"])
	helmRole := parseClusterRole(t, docs["ClusterRole/chart-test-ojs-operator-manager-role"])

	for _, manifest := range []struct {
		name       string
		deployment appsv1.Deployment
		role       rbacv1.ClusterRole
	}{
		{name: "raw", deployment: rawDeployment, role: rawRole},
		{name: "Helm", deployment: helmDeployment, role: helmRole},
	} {
		args := manifest.deployment.Spec.Template.Spec.Containers[0].Args
		if !hasString(args, "--leader-elect") {
			t.Errorf("%s Deployment args = %v, want --leader-elect", manifest.name, args)
		}
		assertRuleVerbs(t, manifest.role,
			"coordination.k8s.io", "leases",
			[]string{"get", "list", "watch", "create", "update", "patch", "delete"})
		assertRuleVerbs(t, manifest.role,
			"policy", "poddisruptionbudgets",
			[]string{"get", "list", "watch", "create", "update", "patch", "delete"})
	}

	if got, ok := envValue(rawDeployment, "ENABLE_WEBHOOKS"); !ok || got != "false" {
		t.Errorf("raw ENABLE_WEBHOOKS = %q, present = %t; want false", got, ok)
	}
	if _, ok := envValue(rawDeployment, "WEBHOOK_CERT_DIR"); ok {
		t.Error("raw Deployment must not set WEBHOOK_CERT_DIR without TLS provisioning")
	}
	rawPodSpec := rawDeployment.Spec.Template.Spec
	for _, volume := range rawPodSpec.Volumes {
		if volume.Name == "webhook-cert" {
			t.Error("raw Deployment must not render a webhook-cert volume")
		}
	}
	for _, mount := range rawPodSpec.Containers[0].VolumeMounts {
		if mount.Name == "webhook-cert" {
			t.Error("raw Deployment must not mount a webhook-cert volume")
		}
	}

	if got, ok := envValue(helmDeployment, "ENABLE_WEBHOOKS"); !ok || got != "true" {
		t.Errorf("Helm ENABLE_WEBHOOKS = %q, present = %t; want true", got, ok)
	}
	if got, ok := envValue(helmDeployment, "WEBHOOK_CERT_DIR"); !ok || got == "" {
		t.Errorf("Helm WEBHOOK_CERT_DIR = %q, present = %t; want provisioned cert directory", got, ok)
	}
}

func TestRenderDefaults_ServiceAccountAndRBACPresent(t *testing.T) {
	requireHelm(t)
	rendered := renderChart(t)
	docs := docsByKindName(rendered)

	if _, ok := docs["ServiceAccount/chart-test-ojs-operator"]; !ok {
		t.Errorf("expected ServiceAccount to render by default; got kinds: %v", keys(docs))
	}
	if _, ok := docs["ClusterRole/chart-test-ojs-operator-manager-role"]; !ok {
		t.Errorf("expected ClusterRole to render by default; got kinds: %v", keys(docs))
	}
	if _, ok := docs["ClusterRoleBinding/chart-test-ojs-operator-manager-rolebinding"]; !ok {
		t.Errorf("expected ClusterRoleBinding to render by default; got kinds: %v", keys(docs))
	}
	if _, ok := docs["Service/chart-test-ojs-operator-webhook-service"]; !ok {
		t.Errorf("expected webhook Service to render when webhook.enabled=true (default); got kinds: %v", keys(docs))
	}
	if _, ok := docs["ValidatingWebhookConfiguration/chart-test-ojs-operator-validating-webhook"]; !ok {
		t.Errorf("expected ValidatingWebhookConfiguration to render by default; got kinds: %v", keys(docs))
	}
}

func TestRenderServiceAccountAndRBACDisabled(t *testing.T) {
	requireHelm(t)
	rendered := renderChart(t, "serviceAccount.create=false", "rbac.create=false", "webhook.enabled=false")
	docs := docsByKindName(rendered)

	if _, ok := docs["ServiceAccount/chart-test-ojs-operator"]; ok {
		t.Error("expected no ServiceAccount when serviceAccount.create=false")
	}
	if _, ok := docs["ClusterRole/chart-test-ojs-operator-manager-role"]; ok {
		t.Error("expected no ClusterRole when rbac.create=false")
	}
	if _, ok := docs["ClusterRoleBinding/chart-test-ojs-operator-manager-rolebinding"]; ok {
		t.Error("expected no ClusterRoleBinding when rbac.create=false")
	}
}

func TestRenderWebhookDisabled_NoWebhookResources(t *testing.T) {
	requireHelm(t)
	rendered := renderChart(t, "webhook.enabled=false")
	docs := docsByKindName(rendered)
	deployment := docs["Deployment/chart-test-ojs-operator"]

	if _, ok := docs["Service/chart-test-ojs-operator-webhook-service"]; ok {
		t.Error("expected no webhook Service when webhook.enabled=false")
	}
	if _, ok := docs["ValidatingWebhookConfiguration/chart-test-ojs-operator-validating-webhook"]; ok {
		t.Error("expected no ValidatingWebhookConfiguration when webhook.enabled=false")
	}
	if _, ok := docs["Certificate/chart-test-ojs-operator-serving-cert"]; ok {
		t.Error("expected no cert-manager Certificate when webhook.enabled=false")
	}
	if _, ok := docs["Issuer/chart-test-ojs-operator-selfsigned-issuer"]; ok {
		t.Error("expected no self-signed Issuer when webhook.enabled=false")
	}
	if !strings.Contains(deployment, "name: ENABLE_WEBHOOKS\n              value: \"false\"") {
		t.Errorf("expected disabled Deployment to set ENABLE_WEBHOOKS=false; deployment:\n%s", deployment)
	}
	for _, unexpected := range []string{"WEBHOOK_CERT_DIR", "name: webhook-cert", "webhook-server-cert", "mountPath: /tmp/k8s-webhook-server/serving-certs"} {
		if strings.Contains(deployment, unexpected) {
			t.Errorf("expected disabled Deployment to omit %q; deployment:\n%s", unexpected, deployment)
		}
	}
}

func TestRenderWebhookEnabledCertManagerDisabled(t *testing.T) {
	requireHelm(t)
	rendered := renderChart(t, "webhook.enabled=true", "webhook.certManager.enabled=false")
	docs := docsByKindName(rendered)

	if _, ok := docs["Service/chart-test-ojs-operator-webhook-service"]; !ok {
		t.Error("expected webhook Service when webhook.enabled=true even with cert-manager disabled")
	}
	if _, ok := docs["ValidatingWebhookConfiguration/chart-test-ojs-operator-validating-webhook"]; !ok {
		t.Error("expected ValidatingWebhookConfiguration when webhook.enabled=true")
	}
	if _, ok := docs["Certificate/chart-test-ojs-operator-serving-cert"]; ok {
		t.Error("expected no cert-manager Certificate when webhook.certManager.enabled=false")
	}
	if _, ok := docs["Issuer/chart-test-ojs-operator-selfsigned-issuer"]; ok {
		t.Error("expected no self-signed Issuer when webhook.certManager.enabled=false")
	}
	deployment := docs["Deployment/chart-test-ojs-operator"]
	for _, expected := range []string{
		"name: ENABLE_WEBHOOKS\n              value: \"true\"",
		"name: WEBHOOK_CERT_DIR\n              value: \"/tmp/k8s-webhook-server/serving-certs\"",
		"mountPath: /tmp/k8s-webhook-server/serving-certs",
		"secretName: chart-test-ojs-operator-webhook-server-cert",
	} {
		if !strings.Contains(deployment, expected) {
			t.Errorf("expected enabled Deployment to contain %q; deployment:\n%s", expected, deployment)
		}
	}
}

func TestRenderWebhookEnabled_DefaultIssuerAndStartupConfiguration(t *testing.T) {
	requireHelm(t)
	rendered := renderChart(t)
	docs := docsByKindName(rendered)

	certificate := docs["Certificate/chart-test-ojs-operator-serving-cert"]
	if !strings.Contains(certificate, "name: chart-test-ojs-operator-selfsigned-issuer") {
		t.Errorf("expected Certificate to reference rendered default issuer; certificate:\n%s", certificate)
	}
	if _, ok := docs["Issuer/chart-test-ojs-operator-selfsigned-issuer"]; !ok {
		t.Errorf("expected default self-signed Issuer; got kinds: %v", keys(docs))
	}

	deployment := docs["Deployment/chart-test-ojs-operator"]
	for _, expected := range []string{
		"- --leader-elect",
		"name: ENABLE_WEBHOOKS\n              value: \"true\"",
		"name: WEBHOOK_CERT_DIR\n              value: \"/tmp/k8s-webhook-server/serving-certs\"",
		"mountPath: /tmp/k8s-webhook-server/serving-certs",
		"secretName: chart-test-ojs-operator-webhook-server-cert",
	} {
		if !strings.Contains(deployment, expected) {
			t.Errorf("expected enabled Deployment to contain %q; deployment:\n%s", expected, deployment)
		}
	}
}

func TestRenderWebhookEnabled_CustomIssuer(t *testing.T) {
	requireHelm(t)
	rendered := renderChart(t,
		"webhook.enabled=true",
		"webhook.certManager.enabled=true",
		"webhook.certManager.issuerRef.kind=ClusterIssuer",
		"webhook.certManager.issuerRef.name=platform-issuer",
	)
	docs := docsByKindName(rendered)

	certificate := docs["Certificate/chart-test-ojs-operator-serving-cert"]
	if !strings.Contains(certificate, "kind: ClusterIssuer") || !strings.Contains(certificate, "name: platform-issuer") {
		t.Errorf("expected Certificate to preserve custom issuerRef; certificate:\n%s", certificate)
	}
	if _, ok := docs["Issuer/chart-test-ojs-operator-selfsigned-issuer"]; ok {
		t.Error("expected no generated self-signed Issuer when a custom issuer name is configured")
	}
}

func TestRenderServiceMonitorRBACToggle(t *testing.T) {
	requireHelm(t)

	enabled := renderChart(t, "serviceMonitor.rbac.enabled=true")
	if !strings.Contains(enabled, "servicemonitors") {
		t.Error("expected ClusterRole to include servicemonitors rule when serviceMonitor.rbac.enabled=true")
	}

	disabled := renderChart(t, "serviceMonitor.rbac.enabled=false")
	if strings.Contains(disabled, "servicemonitors") {
		t.Error("expected ClusterRole to omit servicemonitors rule when serviceMonitor.rbac.enabled=false")
	}
}

func TestRenderClusterRoleIncludesPodDisruptionBudgetPermissions(t *testing.T) {
	requireHelm(t)
	rendered := renderChart(t)
	role := docsByKindName(rendered)["ClusterRole/chart-test-ojs-operator-manager-role"]

	want := `  - apiGroups:
      - policy
    resources:
      - poddisruptionbudgets
    verbs:
      - create
      - delete
      - get
      - list
      - patch
      - update
      - watch`
	if !strings.Contains(role, want) {
		t.Errorf("expected ClusterRole to include full policy/poddisruptionbudgets permissions; role:\n%s", role)
	}
}

func TestRenderPodDisruptionBudgetVariants(t *testing.T) {
	requireHelm(t)

	off := renderChart(t, "podDisruptionBudget.enabled=false")
	if strings.Contains(off, "kind: PodDisruptionBudget") {
		t.Error("expected no PodDisruptionBudget when podDisruptionBudget.enabled=false")
	}

	minAvail := renderChart(t, "podDisruptionBudget.enabled=true", "podDisruptionBudget.minAvailable=2")
	if !strings.Contains(minAvail, "minAvailable: 2") {
		t.Error("expected minAvailable: 2 to render in PodDisruptionBudget")
	}

	maxUnavail := renderChart(t, "podDisruptionBudget.enabled=true", "podDisruptionBudget.minAvailable=null", "podDisruptionBudget.maxUnavailable=1")
	if !strings.Contains(maxUnavail, "maxUnavailable: 1") {
		t.Error("expected maxUnavailable: 1 to render in PodDisruptionBudget")
	}
}

func TestRenderCRDsToggle(t *testing.T) {
	requireHelm(t)

	installed := renderChart(t, "crds.install=true")
	if !strings.Contains(installed, "kind: CustomResourceDefinition") {
		t.Error("expected CRDs to render when crds.install=true")
	}

	skipped := renderChart(t, "crds.install=false")
	if strings.Contains(skipped, "kind: CustomResourceDefinition") {
		t.Error("expected no CRDs to render when crds.install=false")
	}
}

func TestRenderedOJSClusterCRDPodDisruptionBudgetSchema(t *testing.T) {
	requireHelm(t)

	rendered := renderChart(t, "crds.install=true")
	doc := docsByKindName(rendered)["CustomResourceDefinition/ojsclusters.ojs.openjobspec.dev"]
	if doc == "" {
		t.Fatal("rendered OJSCluster CRD is missing")
	}

	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal([]byte(doc), crd); err != nil {
		t.Fatalf("parse rendered OJSCluster CRD: %v", err)
	}

	assertRenderedPDBSchemaAndSample(t, crd.Spec.Versions[0].Schema.OpenAPIV3Schema)
}

func assertRenderedPDBSchemaAndSample(t *testing.T, schema *apiextensionsv1.JSONSchemaProps) {
	t.Helper()

	specSchema := schema.Properties["spec"]
	imageSchema := specSchema.Properties["image"]
	if imageSchema.Default == nil {
		t.Fatal("spec.image default is missing")
	}
	var defaultImage string
	if err := json.Unmarshal(imageSchema.Default.Raw, &defaultImage); err != nil {
		t.Fatalf("decode spec.image default: %v", err)
	}
	if defaultImage != "ghcr.io/openjobspec/ojs-server:v0.5.0" {
		t.Errorf("spec.image default = %q, want 0.5.0 image", defaultImage)
	}

	pdbSchema, ok := specSchema.Properties["podDisruptionBudget"]
	if !ok {
		t.Fatal("spec.podDisruptionBudget schema is missing")
	}
	if pdbSchema.Type != "object" {
		t.Errorf("podDisruptionBudget type = %q, want object", pdbSchema.Type)
	}
	if pdbSchema.Description != "PodDisruptionBudget configures disruption budgets for HA." {
		t.Errorf("podDisruptionBudget description = %q", pdbSchema.Description)
	}
	if len(pdbSchema.Required) != 0 {
		t.Errorf("podDisruptionBudget fields must remain optional, required = %v", pdbSchema.Required)
	}

	expected := map[string]struct {
		typ         string
		format      string
		description string
	}{
		"enabled": {
			typ:         "boolean",
			description: "Enabled creates a PodDisruptionBudget for the server deployment (default true for replicas > 1).",
		},
		"minAvailable": {
			typ:         "integer",
			format:      "int32",
			description: "MinAvailable is the minimum number of pods that must remain available.",
		},
		"maxUnavailable": {
			typ:         "integer",
			format:      "int32",
			description: "MaxUnavailable is the maximum number of pods that can be unavailable.",
		},
	}
	for name, want := range expected {
		property, ok := pdbSchema.Properties[name]
		if !ok {
			t.Errorf("podDisruptionBudget.%s schema is missing", name)
			continue
		}
		if property.Type != want.typ || property.Format != want.format {
			t.Errorf("podDisruptionBudget.%s type/format = %q/%q, want %q/%q", name, property.Type, property.Format, want.typ, want.format)
		}
		if property.Description != want.description {
			t.Errorf("podDisruptionBudget.%s description = %q", name, property.Description)
		}
		if name != "enabled" && (property.Minimum == nil || *property.Minimum != 0) {
			t.Errorf("podDisruptionBudget.%s minimum = %v, want 0", name, property.Minimum)
		}
	}

	internalSchema := &apiextensions.JSONSchemaProps{}
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(schema, internalSchema, nil); err != nil {
		t.Fatalf("convert rendered CRD schema: %v", err)
	}

	sample := renderedSampleOJSCluster(t)
	if err := validateRenderedOpenAPIValue(sample, schema, "ojsCluster"); err != nil {
		t.Fatalf("sample OJSCluster failed rendered OpenAPI validation: %v", err)
	}
	for _, field := range []string{"minAvailable", "maxUnavailable"} {
		invalid := renderedSampleOJSCluster(t)
		invalid["spec"].(map[string]interface{})["podDisruptionBudget"].(map[string]interface{})[field] = float64(-1)
		if err := validateRenderedOpenAPIValue(invalid, schema, "ojsCluster"); err == nil {
			t.Errorf("negative podDisruptionBudget.%s passed rendered OpenAPI validation", field)
		}
	}

	structural, err := structuralschema.NewStructural(internalSchema)
	if err != nil {
		t.Fatalf("build rendered structural CRD schema: %v", err)
	}
	pruning.Prune(sample, structural, true)

	spec := sample["spec"].(map[string]interface{})
	gotPDB, ok := spec["podDisruptionBudget"].(map[string]interface{})
	if !ok {
		t.Fatalf("podDisruptionBudget was pruned from rendered sample: %#v", spec)
	}
	wantPDB := map[string]interface{}{
		"enabled":        false,
		"minAvailable":   float64(0),
		"maxUnavailable": float64(0),
	}
	if !reflect.DeepEqual(gotPDB, wantPDB) {
		t.Errorf("retained podDisruptionBudget = %#v, want %#v", gotPDB, wantPDB)
	}
}

func renderedSampleOJSCluster(t *testing.T) map[string]interface{} {
	t.Helper()

	const sample = `{
		"apiVersion":"ojs.openjobspec.dev/v1alpha1",
		"kind":"OJSCluster",
		"metadata":{"name":"pdb-schema-test"},
		"spec":{
			"backend":{"type":"redis"},
			"podDisruptionBudget":{
				"enabled":false,
				"minAvailable":0,
				"maxUnavailable":0
			}
		}
	}`
	obj := map[string]interface{}{}
	if err := json.Unmarshal([]byte(sample), &obj); err != nil {
		t.Fatalf("decode sample OJSCluster: %v", err)
	}
	return obj
}

func validateRenderedOpenAPIValue(value interface{}, schema *apiextensionsv1.JSONSchemaProps, path string) error {
	switch schema.Type {
	case "object":
		obj, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s: got %T, want object", path, value)
		}
		for _, required := range schema.Required {
			if _, ok := obj[required]; !ok {
				return fmt.Errorf("%s.%s: required field is missing", path, required)
			}
		}
		for name, propertySchema := range schema.Properties {
			if property, ok := obj[name]; ok {
				propertySchema := propertySchema
				if err := validateRenderedOpenAPIValue(property, &propertySchema, path+"."+name); err != nil {
					return err
				}
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: got %T, want boolean", path, value)
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number {
			return fmt.Errorf("%s: got %#v, want integer", path, value)
		}
		if schema.Minimum != nil && number < *schema.Minimum {
			return fmt.Errorf("%s: got %v, minimum is %v", path, number, *schema.Minimum)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: got %T, want string", path, value)
		}
	}
	return nil
}

// TestRenderNoDoubleDashSeparatorArtifacts guards against the class of bug
// this chart has previously had: a `{{- if ... -}}` immediately after a bare
// `---` document separator can eat the newline between them and glue the
// separator onto the next document's first line (e.g. "---apiVersion: ...").
// Every rendered document must start cleanly on its own line.
func TestRenderNoDoubleDashSeparatorArtifacts(t *testing.T) {
	requireHelm(t)
	rendered := renderChart(t)

	for _, line := range strings.Split(rendered, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "---" {
			continue
		}
		if strings.HasPrefix(trimmed, "---") && len(trimmed) > 3 {
			t.Errorf("found document separator glued to content (missing newline): %q", trimmed)
		}
	}
}

func keys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

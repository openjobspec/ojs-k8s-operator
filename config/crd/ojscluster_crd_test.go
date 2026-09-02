package crd

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"sigs.k8s.io/yaml"
)

func TestOJSClusterCRDPodDisruptionBudgetSchema(t *testing.T) {
	data, err := os.ReadFile("ojscluster-crd.yaml")
	if err != nil {
		t.Fatalf("read OJSCluster CRD: %v", err)
	}

	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(data, crd); err != nil {
		t.Fatalf("parse OJSCluster CRD: %v", err)
	}

	assertPDBSchemaAndSample(t, crd.Spec.Versions[0].Schema.OpenAPIV3Schema)
}

func assertPDBSchemaAndSample(t *testing.T, schema *apiextensionsv1.JSONSchemaProps) {
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
		t.Fatalf("convert CRD schema: %v", err)
	}

	sample := sampleOJSCluster(t)
	if err := validateOpenAPIValue(sample, schema, "ojsCluster"); err != nil {
		t.Fatalf("sample OJSCluster failed parsed OpenAPI validation: %v", err)
	}
	for _, field := range []string{"minAvailable", "maxUnavailable"} {
		invalid := sampleOJSCluster(t)
		invalid["spec"].(map[string]interface{})["podDisruptionBudget"].(map[string]interface{})[field] = float64(-1)
		if err := validateOpenAPIValue(invalid, schema, "ojsCluster"); err == nil {
			t.Errorf("negative podDisruptionBudget.%s passed parsed OpenAPI validation", field)
		}
	}

	structural, err := structuralschema.NewStructural(internalSchema)
	if err != nil {
		t.Fatalf("build structural CRD schema: %v", err)
	}
	pruning.Prune(sample, structural, true)

	spec := sample["spec"].(map[string]interface{})
	gotPDB, ok := spec["podDisruptionBudget"].(map[string]interface{})
	if !ok {
		t.Fatalf("podDisruptionBudget was pruned from sample: %#v", spec)
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

func sampleOJSCluster(t *testing.T) map[string]interface{} {
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

func validateOpenAPIValue(value interface{}, schema *apiextensionsv1.JSONSchemaProps, path string) error {
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
				if err := validateOpenAPIValue(property, &propertySchema, path+"."+name); err != nil {
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

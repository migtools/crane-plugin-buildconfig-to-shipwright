package buildconfig

import (
	"strings"
	"testing"

	"github.com/konveyor/crane-lib/transform"
	buildv1 "github.com/openshift/api/build/v1"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func trustedCABuildConfigRequest(strategyType, strategyKey string, mountTrustedCA bool, volumes []interface{}) transform.PluginRequest {
	strategy := map[string]interface{}{}
	if volumes != nil {
		strategy["volumes"] = volumes
	}
	return transform.PluginRequest{
		Unstructured: unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "build.openshift.io/v1",
			"kind":       "BuildConfig",
			"metadata": map[string]interface{}{
				"name":      "trusted-ca-app",
				"namespace": "myns",
			},
			"spec": map[string]interface{}{
				"mountTrustedCA": mountTrustedCA,
				"source": map[string]interface{}{
					"type": "Git",
					"git": map[string]interface{}{
						"uri": "https://github.com/example/myapp.git",
					},
				},
				"strategy": map[string]interface{}{
					"type":      strategyType,
					strategyKey: strategy,
				},
				"output": map[string]interface{}{
					"to": map[string]interface{}{
						"kind": "DockerImage",
						"name": "quay.io/example/myapp:latest",
					},
				},
			},
		}},
	}
}

func findConfigMap(resp transform.PluginResponse, name string) *unstructured.Unstructured {
	for i := range resp.NewResources {
		r := resp.NewResources[i]
		if r.GetKind() == "ConfigMap" && r.GetName() == name {
			return &r
		}
	}
	return nil
}

func TestConvertMountTrustedCA(t *testing.T) {
	for _, tt := range []struct {
		name        string
		strategy    string
		strategyKey string
	}{
		{"docker strategy", "Docker", "dockerStrategy"},
		{"source strategy", "Source", "sourceStrategy"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logger, hook := logrustest.NewNullLogger()
			plugin := &BuildConfigTransformPlugin{Log: logger}
			resp, err := plugin.Run(trustedCABuildConfigRequest(tt.strategy, tt.strategyKey, true, nil))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			b := decodeBuild(t, resp)
			if len(b.Spec.Volumes) != 1 {
				t.Fatalf("expected 1 Build spec volume, got %d: %+v", len(b.Spec.Volumes), b.Spec.Volumes)
			}
			vol := b.Spec.Volumes[0]
			if vol.Name != TrustedCAVolumeName {
				t.Errorf("expected volume name %q, got %q", TrustedCAVolumeName, vol.Name)
			}
			if vol.ConfigMap == nil || vol.ConfigMap.Name != TrustedCABundleConfigMapName {
				t.Errorf("expected configMap volume source %q, got %+v", TrustedCABundleConfigMapName, vol.VolumeSource)
			}

			cm := findConfigMap(resp, TrustedCABundleConfigMapName)
			if cm == nil {
				t.Fatalf("expected ConfigMap %q in new resources, got %+v", TrustedCABundleConfigMapName, resp.NewResources)
			}
			if cm.GetNamespace() != "myns" {
				t.Errorf("expected ConfigMap namespace myns, got %q", cm.GetNamespace())
			}
			if v := cm.GetLabels()[InjectTrustedCABundleLabel]; v != "true" {
				t.Errorf("expected label %s=true on ConfigMap, got labels %+v", InjectTrustedCABundleLabel, cm.GetLabels())
			}

			// Shipped strategies define the trusted-ca volume: no warning expected.
			for _, entry := range hook.AllEntries() {
				if strings.Contains(entry.Message, "not a shipped strategy") {
					t.Errorf("unexpected non-shipped-strategy warning: %q", entry.Message)
				}
			}
		})
	}
}

func TestConvertMountTrustedCADisabled(t *testing.T) {
	logger, _ := logrustest.NewNullLogger()
	plugin := &BuildConfigTransformPlugin{Log: logger}
	resp, err := plugin.Run(trustedCABuildConfigRequest("Docker", "dockerStrategy", false, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := decodeBuild(t, resp)
	if len(b.Spec.Volumes) != 0 {
		t.Errorf("expected no Build spec volumes, got %+v", b.Spec.Volumes)
	}
	if cm := findConfigMap(resp, TrustedCABundleConfigMapName); cm != nil {
		t.Errorf("expected no trusted CA ConfigMap, got %+v", cm)
	}
}

func TestConvertMountTrustedCAVolumeNameCollision(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	plugin := &BuildConfigTransformPlugin{Log: logger}
	request := trustedCABuildConfigRequest("Docker", "dockerStrategy", true, []interface{}{
		map[string]interface{}{
			"name":   TrustedCAVolumeName,
			"source": map[string]interface{}{"type": "ConfigMap", "configMap": map[string]interface{}{"name": "my-own-ca"}},
		},
	})

	resp, err := plugin.Run(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The explicit strategy volume wins; the mapping is skipped entirely.
	b := decodeBuild(t, resp)
	if len(b.Spec.Volumes) != 1 {
		t.Fatalf("expected 1 Build spec volume, got %d: %+v", len(b.Spec.Volumes), b.Spec.Volumes)
	}
	if b.Spec.Volumes[0].ConfigMap == nil || b.Spec.Volumes[0].ConfigMap.Name != "my-own-ca" {
		t.Errorf("expected explicit volume backed by my-own-ca to be kept, got %+v", b.Spec.Volumes[0])
	}
	if cm := findConfigMap(resp, TrustedCABundleConfigMapName); cm != nil {
		t.Errorf("expected no trusted CA ConfigMap when mapping is skipped, got %+v", cm)
	}

	var sawSkip bool
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "skipping the trusted CA mapping") {
			sawSkip = true
			if entry.Level != logrus.WarnLevel {
				t.Errorf("collision message should be warn-level, got %s", entry.Level)
			}
		}
	}
	if !sawSkip {
		t.Error("expected warn-and-skip message for trusted-ca volume name collision")
	}
}

func TestConvertMountTrustedCACustomStrategyWarning(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	mountTrustedCA := true
	bc := &buildv1.BuildConfig{}
	bc.Name = "trusted-ca-app"
	bc.Namespace = "myns"
	bc.Spec.MountTrustedCA = &mountTrustedCA
	bc.Spec.Strategy = buildv1.BuildStrategy{
		Type:           buildv1.DockerBuildStrategyType,
		DockerStrategy: &buildv1.DockerBuildStrategy{},
	}
	bc.Spec.Output.To = &corev1.ObjectReference{Kind: "DockerImage", Name: "quay.io/example/myapp:latest"}

	c := &Converter{
		Log:  logger,
		Opts: PluginOptionalFields{StrategyMapping: map[string]string{"docker": "my-custom-strategy"}},
	}
	result, err := c.Convert(bc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) < 2 {
		t.Fatalf("expected Build and ConfigMap, got %+v", result)
	}

	// The volume is still appended — the user may have added trusted-ca to
	// their custom strategy — but a warning must flag the requirement.
	var sawWarning bool
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "not a shipped strategy") && strings.Contains(entry.Message, "my-custom-strategy") {
			sawWarning = true
			if entry.Level != logrus.WarnLevel {
				t.Errorf("non-shipped-strategy message should be warn-level, got %s", entry.Level)
			}
		}
	}
	if !sawWarning {
		t.Error("expected non-shipped-strategy warning for custom strategy mapping")
	}
}

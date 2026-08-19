package buildconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/konveyor/crane-lib/transform"
	buildv1 "github.com/openshift/api/build/v1"
	shipwrightv1beta1 "github.com/shipwright-io/build/pkg/apis/build/v1beta1"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func volumesBuildConfigRequest(strategyType string, strategyKey string, volumes []interface{}) transform.PluginRequest {
	return transform.PluginRequest{
		Unstructured: unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "build.openshift.io/v1",
			"kind":       "BuildConfig",
			"metadata": map[string]interface{}{
				"name":      "volumes-app",
				"namespace": "myns",
			},
			"spec": map[string]interface{}{
				"source": map[string]interface{}{
					"type": "Git",
					"git": map[string]interface{}{
						"uri": "https://github.com/example/myapp.git",
					},
				},
				"strategy": map[string]interface{}{
					"type": strategyType,
					strategyKey: map[string]interface{}{
						"volumes": volumes,
					},
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

func decodeBuild(t *testing.T, resp transform.PluginResponse) *shipwrightv1beta1.Build {
	t.Helper()
	if len(resp.NewResources) < 1 {
		t.Fatal("expected at least 1 new resource")
	}
	b := &shipwrightv1beta1.Build{}
	jsonBytes, _ := json.Marshal(resp.NewResources[0].Object)
	if err := json.Unmarshal(jsonBytes, b); err != nil {
		t.Fatalf("failed to decode Build: %v", err)
	}
	return b
}

func TestConvertDockerStrategyVolumes(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	plugin := &BuildConfigTransformPlugin{Log: logger}
	request := volumesBuildConfigRequest("Docker", "dockerStrategy", []interface{}{
		map[string]interface{}{
			"name":   "secret-vol",
			"source": map[string]interface{}{"type": "Secret", "secret": map[string]interface{}{"secretName": "my-secret"}},
		},
		map[string]interface{}{
			"name":   "config-vol",
			"source": map[string]interface{}{"type": "ConfigMap", "configMap": map[string]interface{}{"name": "my-config"}},
		},
		map[string]interface{}{
			"name":   "csi-vol",
			"source": map[string]interface{}{"type": "CSI", "csi": map[string]interface{}{"driver": "inline.storage.kubernetes.io"}},
		},
	})

	resp, err := plugin.Run(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := decodeBuild(t, resp)

	// Supported volumes are converted; unsupported CSI volume is skipped.
	if len(b.Spec.Volumes) != 2 {
		t.Fatalf("expected 2 Build spec volumes, got %d: %+v", len(b.Spec.Volumes), b.Spec.Volumes)
	}
	if b.Spec.Volumes[0].Name != "secret-vol" || b.Spec.Volumes[0].Secret == nil || b.Spec.Volumes[0].Secret.SecretName != "my-secret" {
		t.Errorf("unexpected secret volume: %+v", b.Spec.Volumes[0])
	}
	if b.Spec.Volumes[1].Name != "config-vol" || b.Spec.Volumes[1].ConfigMap == nil || b.Spec.Volumes[1].ConfigMap.Name != "my-config" {
		t.Errorf("unexpected configMap volume: %+v", b.Spec.Volumes[1])
	}

	var sawSkip, sawSummary bool
	remediation := map[string]bool{}
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, `Skipping volume "csi-vol"`) && strings.Contains(entry.Message, "unsupported volume source type") {
			sawSkip = true
			if entry.Level != logrus.WarnLevel {
				t.Errorf("skip message should be warn-level, got %s", entry.Level)
			}
		}
		if strings.Contains(entry.Message, "Volumes were converted to Build spec volumes") && strings.Contains(entry.Message, "Buildah") {
			sawSummary = true
			if !strings.Contains(entry.Message, "Registered=False") || !strings.Contains(entry.Message, "UndefinedVolume") {
				t.Errorf("summary warning must state the real failure (Registered=False, UndefinedVolume), got %q", entry.Message)
			}
			if !strings.Contains(entry.Message, "docs/volume-migration.md") {
				t.Errorf("summary warning must reference the runbook, got %q", entry.Message)
			}
			if entry.Level != logrus.WarnLevel {
				t.Errorf("summary message should be warn-level, got %s", entry.Level)
			}
		}
		for _, name := range []string{"secret-vol", "config-vol"} {
			if strings.Contains(entry.Message, "add an overridable volume named '"+name+"'") {
				remediation[name] = true
				if !strings.Contains(entry.Message, "UndefinedVolume") ||
					!strings.Contains(entry.Message, "(2) add a volumeMount") ||
					!strings.Contains(entry.Message, "(3) point the Build at the strategy copy") {
					t.Errorf("per-volume remediation for %s incomplete: %q", name, entry.Message)
				}
			}
		}
		// Old wording implying volumes are not converted must be gone.
		if strings.Contains(entry.Message, "Volumes require the Buildah ClusterBuildStrategy") {
			t.Errorf("old volumes warning still emitted: %q", entry.Message)
		}
		// The pre-BUILD-2324 understatement and stale RFE link must be gone.
		if strings.Contains(entry.Message, "only take effect") || strings.Contains(entry.Message, "BUILD-1747") {
			t.Errorf("stale volume warning wording still emitted: %q", entry.Message)
		}
	}
	if !sawSkip {
		t.Error("expected warn-and-skip message for unsupported CSI volume")
	}
	if !sawSummary {
		t.Error("expected UndefinedVolume summary warning for Docker strategy")
	}
	if !remediation["secret-vol"] || !remediation["config-vol"] {
		t.Errorf("expected per-volume remediation warnings for secret-vol and config-vol, got %v", remediation)
	}
}

func TestConvertSourceStrategyVolumes(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	plugin := &BuildConfigTransformPlugin{Log: logger}
	request := volumesBuildConfigRequest("Source", "sourceStrategy", []interface{}{
		map[string]interface{}{
			"name":   "secret-vol",
			"source": map[string]interface{}{"type": "Secret", "secret": map[string]interface{}{"secretName": "my-secret"}},
		},
	})

	resp, err := plugin.Run(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := decodeBuild(t, resp)
	if len(b.Spec.Volumes) != 1 || b.Spec.Volumes[0].Name != "secret-vol" {
		t.Fatalf("expected 1 Build spec volume named secret-vol, got %+v", b.Spec.Volumes)
	}

	sawSummary := false
	sawRemediation := false
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "Volumes were converted to Build spec volumes") && strings.Contains(entry.Message, "Source-to-Image") {
			sawSummary = true
			if !strings.Contains(entry.Message, "Registered=False") || !strings.Contains(entry.Message, "UndefinedVolume") {
				t.Errorf("summary warning must state the real failure (Registered=False, UndefinedVolume), got %q", entry.Message)
			}
		}
		if strings.Contains(entry.Message, "add an overridable volume named 'secret-vol'") {
			sawRemediation = true
		}
	}
	if !sawSummary {
		t.Error("expected UndefinedVolume summary warning for Source strategy")
	}
	if !sawRemediation {
		t.Error("expected per-volume remediation warning for secret-vol")
	}
}

func TestConvertStrategyVolumeMounts(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	plugin := &BuildConfigTransformPlugin{Log: logger}
	request := volumesBuildConfigRequest("Docker", "dockerStrategy", []interface{}{
		map[string]interface{}{
			"name":   "secret-vol",
			"source": map[string]interface{}{"type": "Secret", "secret": map[string]interface{}{"secretName": "my-secret"}},
			"mounts": []interface{}{
				map[string]interface{}{"destinationPath": "/etc/npm"},
				map[string]interface{}{"destinationPath": "/etc/pip"},
			},
		},
	})

	if _, err := plugin.Run(request); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sawRemediation := false
	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.ErrorLevel {
			t.Errorf("no error-level logs expected, got %q", entry.Message)
		}
		if strings.Contains(entry.Message, `Volume "secret-vol" was converted`) {
			sawRemediation = true
			if !strings.Contains(entry.Message, "original BuildConfig destination paths: /etc/npm, /etc/pip") {
				t.Errorf("remediation warning should echo destination paths, got %q", entry.Message)
			}
			if !strings.Contains(entry.Message, "(1) add an overridable volume named 'secret-vol'") ||
				!strings.Contains(entry.Message, "overridable: true") ||
				!strings.Contains(entry.Message, "(2) add a volumeMount for 'secret-vol'") ||
				!strings.Contains(entry.Message, "(3) point the Build at the strategy copy via spec.strategy.name") {
				t.Errorf("remediation warning missing 3-step guidance: %q", entry.Message)
			}
			if entry.Level != logrus.WarnLevel {
				t.Errorf("remediation message should be warn-level, got %s", entry.Level)
			}
		}
	}
	if !sawRemediation {
		t.Error("expected per-volume remediation warning with mount paths")
	}
}

func TestConvertStrategyVolumesInvalidNames(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	plugin := &BuildConfigTransformPlugin{Log: logger}
	request := volumesBuildConfigRequest("Docker", "dockerStrategy", []interface{}{
		map[string]interface{}{
			"name":   "",
			"source": map[string]interface{}{"type": "Secret", "secret": map[string]interface{}{"secretName": "unnamed-secret"}},
		},
		map[string]interface{}{
			"name":   "dup-vol",
			"source": map[string]interface{}{"type": "Secret", "secret": map[string]interface{}{"secretName": "first-secret"}},
		},
		map[string]interface{}{
			"name":   "dup-vol",
			"source": map[string]interface{}{"type": "ConfigMap", "configMap": map[string]interface{}{"name": "second-config"}},
		},
	})

	resp, err := plugin.Run(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := decodeBuild(t, resp)

	// Empty-name volume and the duplicate are skipped; first dup-vol wins.
	if len(b.Spec.Volumes) != 1 {
		t.Fatalf("expected 1 Build spec volume, got %d: %+v", len(b.Spec.Volumes), b.Spec.Volumes)
	}
	if b.Spec.Volumes[0].Name != "dup-vol" || b.Spec.Volumes[0].Secret == nil || b.Spec.Volumes[0].Secret.SecretName != "first-secret" {
		t.Errorf("expected first dup-vol (secret) to win, got %+v", b.Spec.Volumes[0])
	}

	var sawEmptySkip, sawDupSkip bool
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "Skipping volume with empty name") {
			sawEmptySkip = true
			if entry.Level != logrus.WarnLevel {
				t.Errorf("empty-name skip should be warn-level, got %s", entry.Level)
			}
		}
		if strings.Contains(entry.Message, `Skipping duplicate volume "dup-vol"`) {
			sawDupSkip = true
			if entry.Level != logrus.WarnLevel {
				t.Errorf("duplicate skip should be warn-level, got %s", entry.Level)
			}
		}
	}
	if !sawEmptySkip {
		t.Error("expected warn-and-skip message for empty volume name")
	}
	if !sawDupSkip {
		t.Error("expected warn-and-skip message for duplicate volume name")
	}
}

func TestConvertStrategyVolumesAllSkipped(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	plugin := &BuildConfigTransformPlugin{Log: logger}
	request := volumesBuildConfigRequest("Docker", "dockerStrategy", []interface{}{
		map[string]interface{}{
			"name":   "csi-vol",
			"source": map[string]interface{}{"type": "CSI", "csi": map[string]interface{}{"driver": "inline.storage.kubernetes.io"}},
		},
	})

	resp, err := plugin.Run(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := decodeBuild(t, resp)
	if len(b.Spec.Volumes) != 0 {
		t.Fatalf("expected no Build spec volumes, got %+v", b.Spec.Volumes)
	}

	// When nothing was converted, neither the summary warning nor a per-volume
	// remediation warning may be emitted — both would falsely claim success.
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "Volumes were converted to Build spec volumes") {
			t.Errorf("conversion-success warning emitted although no volume was converted: %q", entry.Message)
		}
		if strings.Contains(entry.Message, "add an overridable volume named") {
			t.Errorf("per-volume remediation emitted although no volume was converted: %q", entry.Message)
		}
	}
}

func TestConvertBuildVolumeSource(t *testing.T) {
	tests := []struct {
		name    string
		source  buildv1.BuildVolumeSource
		wantErr string
		check   func(t *testing.T, vs corev1.VolumeSource)
	}{
		{
			name: "secret source",
			source: buildv1.BuildVolumeSource{
				Type:   buildv1.BuildVolumeSourceTypeSecret,
				Secret: &corev1.SecretVolumeSource{SecretName: "my-secret"},
			},
			check: func(t *testing.T, vs corev1.VolumeSource) {
				if vs.Secret == nil || vs.Secret.SecretName != "my-secret" {
					t.Errorf("unexpected secret source: %+v", vs)
				}
			},
		},
		{
			name: "configMap source",
			source: buildv1.BuildVolumeSource{
				Type: buildv1.BuildVolumeSourceTypeConfigMap,
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "my-config"},
				},
			},
			check: func(t *testing.T, vs corev1.VolumeSource) {
				if vs.ConfigMap == nil || vs.ConfigMap.Name != "my-config" {
					t.Errorf("unexpected configMap source: %+v", vs)
				}
			},
		},
		{
			name:    "nil secret",
			source:  buildv1.BuildVolumeSource{Type: buildv1.BuildVolumeSourceTypeSecret},
			wantErr: "secret volume source is nil",
		},
		{
			name:    "nil configMap",
			source:  buildv1.BuildVolumeSource{Type: buildv1.BuildVolumeSourceTypeConfigMap},
			wantErr: "configMap volume source is nil",
		},
		{
			name:    "unsupported type",
			source:  buildv1.BuildVolumeSource{Type: buildv1.BuildVolumeSourceTypeCSI},
			wantErr: "unsupported volume source type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, err := convertBuildVolumeSource(tt.source)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.check(t, vs)
		})
	}
}

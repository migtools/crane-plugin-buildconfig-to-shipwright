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

// The request builder names its BuildConfig trusted-ca-app; the converter
// derives the per-Build ConfigMap name from the converted Build's name.
const testTrustedCAConfigMapName = "trusted-ca-app" + TrustedCABundleConfigMapSuffix

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
			if vol.ConfigMap == nil || vol.ConfigMap.Name != testTrustedCAConfigMapName {
				t.Errorf("expected configMap volume source %q, got %+v", testTrustedCAConfigMapName, vol.VolumeSource)
			}
			if vol.ConfigMap != nil && (len(vol.ConfigMap.Items) != 1 || vol.ConfigMap.Items[0].Key != TrustedCABundleKey || vol.ConfigMap.Items[0].Path != TrustedCABundleKey) {
				t.Errorf("expected volume projection restricted to %s, got %+v", TrustedCABundleKey, vol.ConfigMap.Items)
			}

			cm := findConfigMap(resp, testTrustedCAConfigMapName)
			if cm == nil {
				t.Fatalf("expected ConfigMap %q in new resources, got %+v", testTrustedCAConfigMapName, resp.NewResources)
			}
			if cm.GetNamespace() != "myns" {
				t.Errorf("expected ConfigMap namespace myns, got %q", cm.GetNamespace())
			}
			if v := cm.GetLabels()[InjectTrustedCABundleLabel]; v != "true" {
				t.Errorf("expected label %s=true on ConfigMap, got labels %+v", InjectTrustedCABundleLabel, cm.GetLabels())
			}
			if v := cm.GetAnnotations()[ConvertedFromAnnotation]; v == "" {
				t.Errorf("expected %s annotation on ConfigMap for traceability, got %+v", ConvertedFromAnnotation, cm.GetAnnotations())
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
	if cm := findConfigMap(resp, testTrustedCAConfigMapName); cm != nil {
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
	if cm := findConfigMap(resp, testTrustedCAConfigMapName); cm != nil {
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
	// their custom strategy — and the emitted resources must prove it.
	vols, _, err := unstructured.NestedSlice(result[0].Object, "spec", "volumes")
	if err != nil || len(vols) != 1 {
		t.Fatalf("expected 1 Build spec volume on custom-strategy Build, got %v (err %v)", vols, err)
	}
	if name, _, _ := unstructured.NestedString(vols[0].(map[string]interface{}), "name"); name != TrustedCAVolumeName {
		t.Errorf("expected volume %q on Build, got %q", TrustedCAVolumeName, name)
	}
	var sawConfigMap bool
	for _, r := range result[1:] {
		if r.GetKind() == "ConfigMap" && r.GetName() == testTrustedCAConfigMapName {
			sawConfigMap = true
		}
	}
	if !sawConfigMap {
		t.Fatalf("expected ConfigMap %q among converted resources, got %+v", testTrustedCAConfigMapName, result)
	}

	// The warning must state the real outcome per the BUILD-2324 fail-visible
	// contract: Shipwright rejects the Build, it does not sit inert.
	var sawWarning bool
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "not a shipped strategy") && strings.Contains(entry.Message, "my-custom-strategy") {
			sawWarning = true
			if entry.Level != logrus.WarnLevel {
				t.Errorf("non-shipped-strategy message should be warn-level, got %s", entry.Level)
			}
			if !strings.Contains(entry.Message, "UndefinedVolume") || !strings.Contains(entry.Message, "Registered=False") {
				t.Errorf("warning should state the UndefinedVolume rejection outcome, got %q", entry.Message)
			}
		}
	}
	if !sawWarning {
		t.Error("expected non-shipped-strategy warning for custom strategy mapping")
	}
}

func TestConvertMountTrustedCAAbsent(t *testing.T) {
	logger, _ := logrustest.NewNullLogger()
	plugin := &BuildConfigTransformPlugin{Log: logger}
	req := trustedCABuildConfigRequest("Docker", "dockerStrategy", false, nil)
	spec := req.Unstructured.Object["spec"].(map[string]interface{})
	delete(spec, "mountTrustedCA") // field absent → nil-pointer branch
	resp, err := plugin.Run(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := decodeBuild(t, resp)
	if len(b.Spec.Volumes) != 0 {
		t.Errorf("expected no Build spec volumes, got %+v", b.Spec.Volumes)
	}
	if cm := findConfigMap(resp, testTrustedCAConfigMapName); cm != nil {
		t.Errorf("expected no trusted CA ConfigMap, got %+v", cm)
	}
}

func TestConvertMountTrustedCAWithOtherVolumes(t *testing.T) {
	logger, _ := logrustest.NewNullLogger()
	plugin := &BuildConfigTransformPlugin{Log: logger}
	resp, err := plugin.Run(trustedCABuildConfigRequest("Docker", "dockerStrategy", true, []interface{}{
		map[string]interface{}{
			"name":   "other-vol",
			"source": map[string]interface{}{"type": "ConfigMap", "configMap": map[string]interface{}{"name": "other-cm"}},
		},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := decodeBuild(t, resp)
	if len(b.Spec.Volumes) != 2 {
		t.Fatalf("expected 2 Build spec volumes (other-vol + trusted-ca), got %d: %+v", len(b.Spec.Volumes), b.Spec.Volumes)
	}
	names := map[string]bool{}
	for _, v := range b.Spec.Volumes {
		names[v.Name] = true
	}
	if !names["other-vol"] || !names[TrustedCAVolumeName] {
		t.Errorf("expected volumes other-vol and %s, got %+v", TrustedCAVolumeName, names)
	}
	if cm := findConfigMap(resp, testTrustedCAConfigMapName); cm == nil {
		t.Error("expected trusted CA ConfigMap alongside non-colliding volumes")
	}
}

func TestConvertMountTrustedCAUnsupportedSourceCollision(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	plugin := &BuildConfigTransformPlugin{Log: logger}
	// A user-declared trusted-ca volume with an unsupported source is skipped
	// by processStrategyVolumes — the mapping must still defer to it instead
	// of silently substituting the injected cluster bundle.
	resp, err := plugin.Run(trustedCABuildConfigRequest("Docker", "dockerStrategy", true, []interface{}{
		map[string]interface{}{
			"name":   TrustedCAVolumeName,
			"source": map[string]interface{}{"type": "CSI"},
		},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := decodeBuild(t, resp)
	if len(b.Spec.Volumes) != 0 {
		t.Fatalf("expected no Build spec volumes (user volume skipped, mapping deferred), got %+v", b.Spec.Volumes)
	}
	if cm := findConfigMap(resp, testTrustedCAConfigMapName); cm != nil {
		t.Errorf("expected no trusted CA ConfigMap when mapping is skipped, got %+v", cm)
	}
	var sawSkip bool
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "skipping the trusted CA mapping") {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Error("expected warn-and-skip message for user-declared trusted-ca volume with unsupported source")
	}
}

func TestConvertMountTrustedCAPerBuildConfigMaps(t *testing.T) {
	logger, _ := logrustest.NewNullLogger()
	c := &Converter{Log: logger}
	mount := true
	newBC := func(name string) *buildv1.BuildConfig {
		bc := &buildv1.BuildConfig{}
		bc.Name = name
		bc.Namespace = "myns"
		bc.Spec.MountTrustedCA = &mount
		bc.Spec.Strategy = buildv1.BuildStrategy{
			Type:           buildv1.DockerBuildStrategyType,
			DockerStrategy: &buildv1.DockerBuildStrategy{},
		}
		bc.Spec.Output.To = &corev1.ObjectReference{Kind: "DockerImage", Name: "quay.io/example/myapp:latest"}
		return bc
	}
	hasCM := func(result []unstructured.Unstructured, name string) bool {
		for _, r := range result {
			if r.GetKind() == "ConfigMap" && r.GetName() == name {
				return true
			}
		}
		return false
	}

	first, err := c.Convert(newBC("app-one"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := c.Convert(newBC("app-two"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each conversion owns its own ConfigMap, named after its Build.
	for i, tc := range []struct {
		result []unstructured.Unstructured
		want   string
	}{
		{first, "app-one" + TrustedCABundleConfigMapSuffix},
		{second, "app-two" + TrustedCABundleConfigMapSuffix},
	} {
		if !hasCM(tc.result, tc.want) {
			t.Errorf("conversion %d: expected per-Build ConfigMap %q, got %+v", i, tc.want, tc.result)
		}
		vols, _, err := unstructured.NestedSlice(tc.result[0].Object, "spec", "volumes")
		if err != nil || len(vols) != 1 {
			t.Fatalf("conversion %d: expected 1 Build spec volume, got %v (err %v)", i, vols, err)
		}
		cmRef, _, _ := unstructured.NestedString(vols[0].(map[string]interface{}), "configMap", "name")
		if cmRef != tc.want {
			t.Errorf("conversion %d: expected volume to reference its own ConfigMap %q, got %q", i, tc.want, cmRef)
		}
	}
}

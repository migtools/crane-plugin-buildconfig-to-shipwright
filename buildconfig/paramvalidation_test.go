package buildconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/konveyor/crane-lib/transform"
	buildv1 "github.com/openshift/api/build/v1"
	shipwrightv1beta1 "github.com/shipwright-io/build/pkg/apis/build/v1beta1"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// validationReasons are the substrings every validateStrategyParams warning
// carries, so tests can pick those warnings out of everything else Convert
// records.
var validationReasons = []string{"UndefinedParameter", "WrongParameterValueType", "MissingParameterValues", "not validated"}

func validationWarnings(c *Converter) []string {
	var out []string
	for _, w := range c.warnings {
		for _, reason := range validationReasons {
			if strings.Contains(w, reason) {
				out = append(out, w)
				break
			}
		}
	}
	return out
}

// dockerKitchenSink drives every Docker-strategy param emission site in
// converter.go. Combined with the three registry flags it produces all nine
// buildah params the converter can emit.
func dockerKitchenSink() *buildv1.BuildConfig {
	skipLayers := buildv1.ImageOptimizationSkipLayers
	return &buildv1.BuildConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "kitchen-sink", Namespace: "myns"},
		Spec: buildv1.BuildConfigSpec{RunPolicy: buildv1.BuildRunPolicyParallel, CommonSpec: buildv1.CommonSpec{
			Source: buildv1.BuildSource{Type: buildv1.BuildSourceGit, Git: &buildv1.GitBuildSource{URI: "https://github.com/example/app.git"}},
			Strategy: buildv1.BuildStrategy{Type: buildv1.DockerBuildStrategyType, DockerStrategy: &buildv1.DockerBuildStrategy{
				From:                    &corev1.ObjectReference{Kind: "DockerImage", Name: "registry.example.com/base:1"},
				NoCache:                 true,
				ForcePull:               true,
				DockerfilePath:          "Dockerfile.prod",
				BuildArgs:               []corev1.EnvVar{{Name: "A", Value: "1"}},
				ImageOptimizationPolicy: &skipLayers,
			}},
			Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Kind: "DockerImage", Name: "quay.io/example/app:latest"}},
		}},
	}
}

// s2iKitchenSink drives the one Source-strategy param emission site; with the
// registry flags it produces all four source-to-image params the converter can
// emit.
func s2iKitchenSink() *buildv1.BuildConfig {
	return &buildv1.BuildConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "kitchen-sink-s2i", Namespace: "myns"},
		Spec: buildv1.BuildConfigSpec{RunPolicy: buildv1.BuildRunPolicyParallel, CommonSpec: buildv1.CommonSpec{
			Source: buildv1.BuildSource{Type: buildv1.BuildSourceGit, Git: &buildv1.GitBuildSource{URI: "https://github.com/example/app.git"}},
			Strategy: buildv1.BuildStrategy{Type: buildv1.SourceBuildStrategyType, SourceStrategy: &buildv1.SourceBuildStrategy{
				From: corev1.ObjectReference{Kind: "DockerImage", Name: "registry.example.com/builder:1"},
			}},
			Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Kind: "DockerImage", Name: "quay.io/example/app:latest"}},
		}},
	}
}

func registryFlags() PluginOptionalFields {
	return PluginOptionalFields{
		SearchRegistries:   []string{"quay.io"},
		InsecureRegistries: []string{"insecure.local:5000"},
		BlockRegistries:    []string{"blocked.io"},
	}
}

// convertedBuild runs Convert and returns the emitted Build alongside the outcome.
func convertedBuild(t *testing.T, c *Converter, bc *buildv1.BuildConfig) (*shipwrightv1beta1.Build, Outcome) {
	t.Helper()
	resources, outcome := c.Convert(bc)
	for _, u := range resources {
		if u.GetKind() != "Build" {
			continue
		}
		b := &shipwrightv1beta1.Build{}
		raw, err := json.Marshal(u.Object)
		if err != nil {
			t.Fatalf("marshal Build: %v", err)
		}
		if err := json.Unmarshal(raw, b); err != nil {
			t.Fatalf("unmarshal Build: %v", err)
		}
		return b, outcome
	}
	t.Fatalf("no Build emitted; outcome %+v", outcome)
	return nil, outcome
}

func paramNames(b *shipwrightv1beta1.Build) []string {
	names := make([]string, 0, len(b.Spec.ParamValues))
	for _, pv := range b.Spec.ParamValues {
		names = append(names, pv.Name)
	}
	sort.Strings(names)
	return names
}

func TestConvertEmittedParamsMatchBundledSchemas(t *testing.T) {
	tests := []struct {
		name       string
		bc         *buildv1.BuildConfig
		strategy   string
		wantParams []string
	}{
		{
			name:       "every Docker emission site against buildah",
			bc:         dockerKitchenSink(),
			strategy:   "buildah",
			wantParams: []string{"build-args", "dockerfile", "no-cache", "pull", "registries-block", "registries-insecure", "registries-search", "runtime-stage-from", "squash"},
		},
		{
			name:       "every Source emission site against source-to-image",
			bc:         s2iKitchenSink(),
			strategy:   "source-to-image",
			wantParams: []string{"builder-image", "registries-block", "registries-insecure", "registries-search"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := logrustest.NewNullLogger()
			c := &Converter{Log: logger, Opts: registryFlags()}
			b, _ := convertedBuild(t, c, tt.bc)

			if b.Spec.Strategy.Name != tt.strategy {
				t.Fatalf("strategy = %q, want %q", b.Spec.Strategy.Name, tt.strategy)
			}
			if got := paramNames(b); strings.Join(got, ",") != strings.Join(tt.wantParams, ",") {
				t.Errorf("emitted params = %v, want %v (an emission site changed; update this test and the bundle together)", got, tt.wantParams)
			}
			if findings := validateParamValues(loadStrategySchemas()[tt.strategy], b.Spec.ParamValues); !findings.empty() {
				t.Errorf("bundled %s schema rejects the converter's own params: %+v", tt.strategy, findings)
			}
			if got := validationWarnings(c); len(got) != 0 {
				t.Errorf("unexpected validation warnings: %v", got)
			}
		})
	}
}

func TestConvertWarnsWhenStrategyHasNoBundledSchema(t *testing.T) {
	minimalDocker := dockerKitchenSink()
	minimalDocker.Spec.Strategy.DockerStrategy = &buildv1.DockerBuildStrategy{}

	tests := []struct {
		name     string
		bc       *buildv1.BuildConfig
		opts     PluginOptionalFields
		strategy string
		extra    []string
	}{
		{
			name:     "docker override lists every emitted param with its type",
			bc:       dockerKitchenSink(),
			opts:     PluginOptionalFields{StrategyMapping: map[string]string{"docker": "my-buildah"}, SearchRegistries: []string{"quay.io"}, InsecureRegistries: []string{"insecure.local:5000"}, BlockRegistries: []string{"blocked.io"}},
			strategy: "my-buildah",
			extra:    []string{"build-args (array)", "registries-search (array)"},
		},
		{
			name:     "s2i override is validated too",
			bc:       s2iKitchenSink(),
			opts:     PluginOptionalFields{StrategyMapping: map[string]string{"s2i": "my-s2i"}},
			strategy: "my-s2i",
			extra:    []string{"builder-image"},
		},
		{
			name:     "override with nothing emitted says so",
			bc:       minimalDocker,
			opts:     PluginOptionalFields{StrategyMapping: map[string]string{"docker": "my-buildah"}},
			strategy: "my-buildah",
			extra:    []string{"(no params were emitted for this Build)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := logrustest.NewNullLogger()
			c := &Converter{Log: logger, Opts: tt.opts}
			b, outcome := convertedBuild(t, c, tt.bc)

			warnings := validationWarnings(c)
			if len(warnings) != 1 {
				t.Fatalf("validation warnings = %d, want exactly 1: %v", len(warnings), warnings)
			}
			msg := warnings[0]
			for _, want := range append(append([]string{tt.strategy, "not validated", "NoBundledSchema", "myns/"}, paramNames(b)...), tt.extra...) {
				if !strings.Contains(msg, want) {
					t.Errorf("warning lacks %q: %s", want, msg)
				}
			}
			for _, reason := range []string{"UndefinedParameter", "WrongParameterValueType", "MissingParameterValues"} {
				if strings.Contains(msg, reason) {
					t.Errorf("override warning must not carry a per-param reason, got %q in: %s", reason, msg)
				}
			}
			if len(c.warnings) != 1 {
				t.Errorf("conversion recorded %d warnings, want only the validation one: %v", len(c.warnings), c.warnings)
			}
			if outcome.State != OutcomeConvertedWithWarnings {
				t.Errorf("outcome = %s, want %s", outcome.State, OutcomeConvertedWithWarnings)
			}
			if got := b.Annotations[ConversionOutcomeAnnotation]; got != string(OutcomeConvertedWithWarnings) {
				t.Errorf("outcome annotation = %q, want %q", got, OutcomeConvertedWithWarnings)
			}
		})
	}
}

// TestConvertSchemaFindingsFlipOutcome proves, through Convert and against the
// real bundle, that a schema finding is what turns a clean conversion into
// converted-with-warnings: pointing the Docker strategy at source-to-image
// makes every Docker param undefined and builder-image missing, and an S2I
// BuildConfig with no builder image reaches MissingParameterValues on its own.
func TestConvertSchemaFindingsFlipOutcome(t *testing.T) {
	noBuilder := s2iKitchenSink()
	noBuilder.Spec.Strategy.SourceStrategy.From = corev1.ObjectReference{}

	tests := []struct {
		name         string
		bc           *buildv1.BuildConfig
		opts         PluginOptionalFields
		wantWarnings int
		want         []string
	}{
		{
			name:         "docker params against the source-to-image schema",
			bc:           dockerKitchenSink(),
			opts:         PluginOptionalFields{StrategyMapping: map[string]string{"docker": "source-to-image"}},
			wantWarnings: 2,
			want:         []string{"UndefinedParameter", "dockerfile", "no-cache", "MissingParameterValues", "builder-image", `"source-to-image"`, "myns/kitchen-sink"},
		},
		{
			name:         "s2i BuildConfig without a builder image",
			bc:           noBuilder,
			wantWarnings: 1,
			want:         []string{"MissingParameterValues", "builder-image", `"source-to-image"`, "myns/kitchen-sink-s2i"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := logrustest.NewNullLogger()
			c := &Converter{Log: logger, Opts: tt.opts}
			b, outcome := convertedBuild(t, c, tt.bc)

			warnings := validationWarnings(c)
			if len(warnings) != tt.wantWarnings {
				t.Fatalf("validation warnings = %d, want %d: %v", len(warnings), tt.wantWarnings, warnings)
			}
			joined := strings.Join(warnings, "\n")
			for _, want := range tt.want {
				if !strings.Contains(joined, want) {
					t.Errorf("warnings lack %q: %s", want, joined)
				}
			}
			if len(c.warnings) != tt.wantWarnings {
				t.Errorf("conversion recorded %d warnings, want only the validation ones: %v", len(c.warnings), c.warnings)
			}
			if outcome.State != OutcomeConvertedWithWarnings {
				t.Errorf("outcome = %s, want %s", outcome.State, OutcomeConvertedWithWarnings)
			}
			if got := b.Annotations[ConversionOutcomeAnnotation]; got != string(OutcomeConvertedWithWarnings) {
				t.Errorf("outcome annotation = %q, want %q", got, OutcomeConvertedWithWarnings)
			}
		})
	}
}

// TestValidateStrategyParamsWarnings drives the validation step directly with
// hand-built Builds against the real bundled schemas, one per finding class:
// buildah for an undefined name and a wrong type (which no conversion can
// produce), source-to-image for its required builder-image. The Convert wiring
// is covered by the two tests above.
func TestValidateStrategyParamsWarnings(t *testing.T) {
	str := func(s string) *string { return &s }
	build := func(strategy string, values ...shipwrightv1beta1.ParamValue) *shipwrightv1beta1.Build {
		b := &shipwrightv1beta1.Build{}
		b.Spec.Strategy.Name = strategy
		b.Spec.ParamValues = values
		return b
	}
	bc := &buildv1.BuildConfig{ObjectMeta: metav1.ObjectMeta{Name: "crafted", Namespace: "myns"}}

	tests := []struct {
		name  string
		build *shipwrightv1beta1.Build
		want  []string
	}{
		{
			name:  "param the strategy does not declare",
			build: build("buildah", shipwrightv1beta1.ParamValue{Name: "no-such-param", SingleValue: &shipwrightv1beta1.SingleValue{Value: str("x")}}),
			want:  []string{"UndefinedParameter", "no-such-param", `"buildah"`, "myns/crafted"},
		},
		{
			name:  "string param given an array",
			build: build("buildah", shipwrightv1beta1.ParamValue{Name: "dockerfile", Values: []shipwrightv1beta1.SingleValue{{Value: str("D")}}}),
			want:  []string{"WrongParameterValueType", "dockerfile", `"buildah"`, "myns/crafted"},
		},
		{
			name:  "required param not set",
			build: build("source-to-image"),
			want:  []string{"MissingParameterValues", "builder-image", `"source-to-image"`, "myns/crafted"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := logrustest.NewNullLogger()
			c := &Converter{Log: logger}
			c.validateStrategyParams(bc, tt.build)

			if len(c.warnings) != 1 {
				t.Fatalf("warnings = %d, want exactly 1: %v", len(c.warnings), c.warnings)
			}
			for _, want := range tt.want {
				if !strings.Contains(c.warnings[0], want) {
					t.Errorf("warning lacks %q: %s", want, c.warnings[0])
				}
			}
		})
	}
}

// TestConvertFixturesProduceNoValidationWarnings runs the plugin over the E2E
// fixtures the way crane does and checks the bundled schemas accept what comes
// out, so the fixtures keep converting cleanly against the shipped catalog.
func TestConvertFixturesProduceNoValidationWarnings(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("..", "tests", "testdata", "export", "resources", "myapp", "BuildConfig_*.yaml"))
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("no BuildConfig fixtures found: %v", err)
	}
	for _, path := range fixtures {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			obj := map[string]interface{}{}
			if err := yaml.Unmarshal(raw, &obj); err != nil {
				t.Fatal(err)
			}
			logger, hook := logrustest.NewNullLogger()
			plugin := &BuildConfigTransformPlugin{Log: logger}
			resp, err := plugin.Run(transform.PluginRequest{Unstructured: unstructured.Unstructured{Object: obj}})
			if err != nil {
				t.Fatalf("plugin.Run: %v", err)
			}
			if len(resp.NewResources) == 0 {
				t.Fatal("fixture was not converted")
			}
			for _, entry := range hook.AllEntries() {
				for _, reason := range validationReasons {
					if strings.Contains(entry.Message, reason) {
						t.Errorf("fixture produced a validation warning: %s", entry.Message)
					}
				}
			}
		})
	}
}

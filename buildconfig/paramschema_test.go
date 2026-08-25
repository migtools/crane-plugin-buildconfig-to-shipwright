package buildconfig

import (
	"reflect"
	"sort"
	"testing"

	shipwrightv1beta1 "github.com/shipwright-io/build/pkg/apis/build/v1beta1"
)

func TestLoadStrategySchemasBundle(t *testing.T) {
	schemas := loadStrategySchemas()

	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	if want := []string{"buildah", "source-to-image"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("bundled strategies = %v, want %v", names, want)
	}
	if got := len(schemas["buildah"]); got != 11 {
		t.Errorf("buildah declares %d params, want 11", got)
	}
	if got := len(schemas["source-to-image"]); got != 9 {
		t.Errorf("source-to-image declares %d params, want 9", got)
	}
}

func TestBundledSchemasRequireOnlyBuilderImage(t *testing.T) {
	// With no values at all, Missing is exactly the set of required params.
	var required []string
	for name, defs := range loadStrategySchemas() {
		for _, missing := range validateParamValues(defs, nil).Missing {
			required = append(required, name+"/"+missing)
		}
	}
	sort.Strings(required)
	if want := []string{"source-to-image/builder-image"}; !reflect.DeepEqual(required, want) {
		t.Errorf("required params = %v, want %v", required, want)
	}
}

func TestValidateParamValues(t *testing.T) {
	str := func(s string) *string { return &s }
	single := func(name, value string) shipwrightv1beta1.ParamValue {
		return shipwrightv1beta1.ParamValue{Name: name, SingleValue: &shipwrightv1beta1.SingleValue{Value: str(value)}}
	}
	array := func(name string, values ...string) shipwrightv1beta1.ParamValue {
		pv := shipwrightv1beta1.ParamValue{Name: name, Values: []shipwrightv1beta1.SingleValue{}}
		for _, v := range values {
			pv.Values = append(pv.Values, shipwrightv1beta1.SingleValue{Value: str(v)})
		}
		return pv
	}

	// A schema with every shape the checks distinguish: a defaulted string, an
	// untyped (so string) param, a required string, a defaulted array and a
	// required array.
	defs := []shipwrightv1beta1.Parameter{
		{Name: "dockerfile", Type: shipwrightv1beta1.ParameterTypeString, Default: str("Dockerfile")},
		{Name: "untyped", Default: str("")},
		{Name: "builder-image", Type: shipwrightv1beta1.ParameterTypeString},
		{Name: "build-args", Type: shipwrightv1beta1.ParameterTypeArray, Defaults: &[]string{}},
		{Name: "extra-args", Type: shipwrightv1beta1.ParameterTypeArray},
	}
	satisfied := []shipwrightv1beta1.ParamValue{single("builder-image", "img"), array("extra-args", "x")}

	tests := []struct {
		name   string
		values []shipwrightv1beta1.ParamValue
		want   paramFindings
	}{
		{
			name:   "every declared param set with the right type",
			values: append([]shipwrightv1beta1.ParamValue{single("dockerfile", "D"), single("untyped", "u"), array("build-args", "A=1")}, satisfied...),
		},
		{
			name:   "name the strategy does not declare",
			values: append([]shipwrightv1beta1.ParamValue{single("no-cache", "true")}, satisfied...),
			want:   paramFindings{Undefined: []string{"no-cache"}},
		},
		{
			name:   "string param given an array",
			values: append([]shipwrightv1beta1.ParamValue{array("dockerfile", "D")}, satisfied...),
			want:   paramFindings{WrongType: []string{"dockerfile"}},
		},
		{
			name:   "array param given a single value",
			values: append([]shipwrightv1beta1.ParamValue{single("build-args", "A=1")}, satisfied...),
			want:   paramFindings{WrongType: []string{"build-args"}},
		},
		{
			name:   "required string not provided",
			values: []shipwrightv1beta1.ParamValue{array("extra-args", "x")},
			want:   paramFindings{Missing: []string{"builder-image"}},
		},
		{
			name:   "required array not provided",
			values: []shipwrightv1beta1.ParamValue{single("builder-image", "img")},
			want:   paramFindings{Missing: []string{"extra-args"}},
		},
		{
			name:   "required string provided with no value counts as missing",
			values: []shipwrightv1beta1.ParamValue{{Name: "builder-image", SingleValue: &shipwrightv1beta1.SingleValue{}}, array("extra-args", "x")},
			want:   paramFindings{Missing: []string{"builder-image"}},
		},
		{
			name:   "undefined name and missing required reported together, values order then defs order",
			values: []shipwrightv1beta1.ParamValue{single("zeta", "1"), single("alpha", "2")},
			want:   paramFindings{Undefined: []string{"zeta", "alpha"}, Missing: []string{"builder-image", "extra-args"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateParamValues(defs, tt.values)
			if !sameNames(got.Undefined, tt.want.Undefined) || !sameNames(got.WrongType, tt.want.WrongType) || !sameNames(got.Missing, tt.want.Missing) {
				t.Errorf("validateParamValues() = %+v, want %+v", got, tt.want)
			}
		})
	}

	t.Run("param with no type is a string and required without a default", func(t *testing.T) {
		untyped := []shipwrightv1beta1.Parameter{{Name: "untyped-required"}}
		if got := validateParamValues(untyped, nil); !sameNames(got.Missing, []string{"untyped-required"}) {
			t.Errorf("validateParamValues() = %+v, want Missing [untyped-required]", got)
		}
		if got := validateParamValues(untyped, []shipwrightv1beta1.ParamValue{single("untyped-required", "v")}); !got.empty() {
			t.Errorf("validateParamValues() = %+v, want empty", got)
		}
	})

	t.Run("no values against an all-default schema is clean", func(t *testing.T) {
		allDefault := []shipwrightv1beta1.Parameter{defs[0], defs[1], defs[3]}
		if got := validateParamValues(allDefault, nil); !got.empty() {
			t.Errorf("validateParamValues() = %+v, want empty", got)
		}
	})
}

// sameNames compares two name lists, treating nil and empty as equal.
func sameNames(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

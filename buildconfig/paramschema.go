package buildconfig

import (
	"embed"
	"fmt"
	"io/fs"

	shipwrightv1beta1 "github.com/shipwright-io/build/pkg/apis/build/v1beta1"
	"sigs.k8s.io/yaml"
)

// StrategyCatalogRef is the redhat-openshift-builds/strategy-catalog commit the
// files under strategies/ were copied from. hack/update-strategy-schemas.sh
// rewrites it, and its --check mode diffs the bundle against this commit and
// against the catalog's main.
const StrategyCatalogRef = "6e40b96"

//go:embed strategies/*.yaml
var strategyFiles embed.FS

// bundledSchemas is parsed once, at package init, so a corrupt bundle fails
// the process (and go test) at startup rather than in the middle of a
// conversion.
var bundledSchemas = mustLoadStrategySchemas()

// loadStrategySchemas returns the bundled ClusterBuildStrategies' declared
// parameters keyed by metadata.name.
func loadStrategySchemas() map[string][]shipwrightv1beta1.Parameter {
	return bundledSchemas
}

// mustLoadStrategySchemas parses every bundled ClusterBuildStrategy. A file
// that fails to parse is a packaging mistake, not a runtime condition, so it
// panics with the file name rather than letting conversions run against a
// missing schema.
func mustLoadStrategySchemas() map[string][]shipwrightv1beta1.Parameter {
	entries, err := fs.ReadDir(strategyFiles, "strategies")
	if err != nil {
		panic(fmt.Sprintf("bundled strategies: %v", err))
	}
	schemas := make(map[string][]shipwrightv1beta1.Parameter, len(entries))
	for _, entry := range entries {
		path := "strategies/" + entry.Name()
		data, err := strategyFiles.ReadFile(path)
		if err != nil {
			panic(fmt.Sprintf("bundled strategy %s: %v", path, err))
		}
		strategy := &shipwrightv1beta1.ClusterBuildStrategy{}
		if err := yaml.Unmarshal(data, strategy); err != nil {
			panic(fmt.Sprintf("bundled strategy %s: %v", path, err))
		}
		if strategy.Name == "" {
			panic(fmt.Sprintf("bundled strategy %s: metadata.name is empty", path))
		}
		schemas[strategy.Name] = strategy.Spec.Parameters
	}
	return schemas
}

// paramFindings lists, by name, the paramValues a strategy would reject. The
// three fields map one to one onto the reasons Shipwright's Build controller
// reports: UndefinedParameter and WrongParameterValueType when the Build is
// registered, MissingParameterValues when a BuildRun starts.
type paramFindings struct {
	Undefined []string
	WrongType []string
	Missing   []string
}

func (f paramFindings) empty() bool {
	return len(f.Undefined) == 0 && len(f.WrongType) == 0 && len(f.Missing) == 0
}

// validateParamValues mirrors the name, type and required checks of
// shipwright-io/build pkg/validate/params.go (v0.20.11). It is a copy rather
// than an import: pkg/validate pulls in Tekton, knative and client-go and
// takes the plugin binary from 13 MB to 66 MB. Undefined and WrongType follow
// the order of values; Missing follows the order of defs.
//
// It reports every class it finds. Shipwright's Build controller reports only
// the first one (UndefinedParameter, else WrongParameterValueType) and leaves
// MissingParameterValues to the BuildRun controller, so a Build can carry more
// than one warning here where the cluster would show one reason at a time.
// Not mirrored, because no emission site in this package can produce them:
// Shipwright's reserved param names, the incomplete configMap/secret ref
// checks, empty array items, items with more than one value kind, and
// duplicate names (Shipwright takes the first occurrence, this takes the
// last).
func validateParamValues(defs []shipwrightv1beta1.Parameter, values []shipwrightv1beta1.ParamValue) paramFindings {
	byName := make(map[string]*shipwrightv1beta1.Parameter, len(defs))
	for i := range defs {
		byName[defs[i].Name] = &defs[i]
	}
	provided := make(map[string]*shipwrightv1beta1.ParamValue, len(values))

	var findings paramFindings
	for i := range values {
		value := &values[i]
		def, ok := byName[value.Name]
		if !ok {
			findings.Undefined = append(findings.Undefined, value.Name)
			continue
		}
		provided[value.Name] = value
		if isArrayParam(*def) {
			if value.SingleValue != nil {
				findings.WrongType = append(findings.WrongType, value.Name)
			}
		} else if value.Values != nil {
			findings.WrongType = append(findings.WrongType, value.Name)
		}
	}

	for _, def := range defs {
		value := provided[def.Name]
		if isArrayParam(def) {
			if def.Defaults == nil && (value == nil || value.Values == nil) {
				findings.Missing = append(findings.Missing, def.Name)
			}
			continue
		}
		if def.Default == nil && (value == nil || value.SingleValue == nil || hasNoValue(*value.SingleValue)) {
			findings.Missing = append(findings.Missing, def.Name)
		}
	}
	return findings
}

// isArrayParam treats an unset type as string, as Shipwright does.
func isArrayParam(def shipwrightv1beta1.Parameter) bool {
	return def.Type == shipwrightv1beta1.ParameterTypeArray
}

// hasNoValue is true when none of value, configMapValue and secretValue is set.
func hasNoValue(v shipwrightv1beta1.SingleValue) bool {
	return v.Value == nil && v.ConfigMapValue == nil && v.SecretValue == nil
}

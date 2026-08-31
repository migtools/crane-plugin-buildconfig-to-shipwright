package framework

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// RuleViolation represents a conversion rule violation.
type RuleViolation struct {
	Rule        string
	Description string
	Expected    string
	Actual      string
}

func (v RuleViolation) String() string {
	return fmt.Sprintf("[%s] %s: expected '%s', got '%s'", v.Rule, v.Description, v.Expected, v.Actual)
}

// ValidateConversion validates BuildConfig → Shipwright Build conversion rules.
// Loads rules from rules.yaml and evaluates each one.
func ValidateConversion(projectRoot string, bcPath string, buildObj *unstructured.Unstructured) ([]RuleViolation, error) {
	// Load rules from YAML
	rulesPath := filepath.Join(projectRoot, "tests", "rules.yaml")
	ruleSet, err := LoadRules(rulesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load rules: %w", err)
	}

	// Convert Build unstructured to map
	buildMap := buildObj.Object

	// Extract BuildConfig name from Build annotation
	annotations, _ := buildMap["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})
	convertedFrom, _ := annotations["crane.konveyor.io/converted-from"].(string)
	parts := strings.Split(convertedFrom, "/")
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid converted-from annotation: %s", convertedFrom)
	}
	bcName := parts[len(parts)-1]

	// Load matching BuildConfig from file
	bcMap, err := LoadBuildConfigByName(bcPath, bcName)
	if err != nil {
		return nil, fmt.Errorf("failed to load BuildConfig %s: %w", bcName, err)
	}

	// Wrap maps with "bc." and "build." prefixes for rule evaluation
	bcWrapped := map[string]interface{}{"bc": bcMap}
	buildWrapped := map[string]interface{}{"build": buildMap}

	// Evaluate all rules
	var violations []RuleViolation
	for _, rule := range ruleSet.Rules {
		if violation := EvaluateRule(rule, bcWrapped, buildWrapped); violation != nil {
			violations = append(violations, *violation)
		}
	}

	return violations, nil
}

// LoadBuildConfigByName loads a BuildConfig from a multi-document YAML file by name.
func LoadBuildConfigByName(yamlPath, name string) (map[string]interface{}, error) {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	decoder := yaml.NewDecoder(strings.NewReader(string(data)))

	for {
		var doc map[string]interface{}
		if err := decoder.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			// Skip non-parseable documents
			continue
		}

		if len(doc) == 0 {
			continue
		}

		// Check if this is the BuildConfig we're looking for
		kind, _ := doc["kind"].(string)
		if kind != "BuildConfig" {
			continue
		}

		metadata, _ := doc["metadata"].(map[string]interface{})
		docName, _ := metadata["name"].(string)

		if docName == name {
			return doc, nil
		}
	}

	return nil, fmt.Errorf("BuildConfig %s not found in %s", name, yamlPath)
}

// FindBuildConfigByName is deprecated - use LoadBuildConfigByName instead.
// Kept for backward compatibility.
func FindBuildConfigByName(yamlPath, name string) (map[string]interface{}, error) {
	return LoadBuildConfigByName(yamlPath, name)
}

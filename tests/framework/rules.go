package framework

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rule represents a validation rule loaded from YAML.
type Rule struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`

	// Common fields
	Field            string   `yaml:"field"`
	Expected         string   `yaml:"expected"`
	AllowedValues    []string `yaml:"allowed_values"`

	// Conditional fields
	When             *Condition `yaml:"when"`
	Then             *Then      `yaml:"then"`
	WhenBCFieldPresent string   `yaml:"when_bc_field_present"`

	// Field mapping
	BCField          string `yaml:"bc_field"`
	BuildField       string `yaml:"build_field"`
	EqualsBCField    string `yaml:"equals_bc_field"`

	// Annotation rules
	Annotation       string `yaml:"annotation"`
	Pattern          string `yaml:"pattern"`

	// Labels
	BCLabels         string `yaml:"bc_labels"`
	BuildLabels      string `yaml:"build_labels"`

	// Retention limits
	Min              int    `yaml:"min"`
	Max              int    `yaml:"max"`

	// Values
	InValues         []string `yaml:"in_values"`
}

type Condition struct {
	Field    string   `yaml:"field"`
	Equals   string   `yaml:"equals"`
	InValues []string `yaml:"in_values"`
}

type Then struct {
	Field         string `yaml:"field"`
	Expected      string `yaml:"expected"`
	EqualsBCField string `yaml:"equals_bc_field"`
	Annotation    string `yaml:"annotation"`
}

// RuleSet holds all validation rules.
type RuleSet struct {
	Rules []Rule `yaml:"rules"`
}

// LoadRules loads rules from a YAML file.
func LoadRules(rulesPath string) (*RuleSet, error) {
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules file: %w", err)
	}

	var ruleSet RuleSet
	if err := yaml.Unmarshal(data, &ruleSet); err != nil {
		return nil, fmt.Errorf("failed to parse rules YAML: %w", err)
	}

	return &ruleSet, nil
}

// GetFieldValue extracts a nested field value from a map using dot notation.
// Example: "build.spec.strategy.name" → traverses map to get value
func GetFieldValue(obj map[string]interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	current := obj

	for i, part := range parts {
		// Handle array access like "annotations[key]"
		if strings.Contains(part, "[") {
			// Extract map key from brackets
			keyStart := strings.Index(part, "[")
			keyEnd := strings.Index(part, "]")
			if keyStart == -1 || keyEnd == -1 {
				return nil
			}

			mapName := part[:keyStart]
			key := strings.Trim(part[keyStart+1:keyEnd], "\"")

			val, ok := current[mapName]
			if !ok {
				return nil
			}

			mapVal, ok := val.(map[string]interface{})
			if !ok {
				return nil
			}

			return mapVal[key]
		}

		val, ok := current[part]
		if !ok {
			return nil
		}

		// Last part - return value
		if i == len(parts)-1 {
			return val
		}

		// Continue traversing
		current, ok = val.(map[string]interface{})
		if !ok {
			return nil
		}
	}

	return current
}

// GetFieldString is a helper to get field value as string.
func GetFieldString(obj map[string]interface{}, path string) string {
	val := GetFieldValue(obj, path)
	if val == nil {
		return ""
	}
	if str, ok := val.(string); ok {
		return str
	}
	return fmt.Sprintf("%v", val)
}

// GetFieldInt is a helper to get field value as int.
func GetFieldInt(obj map[string]interface{}, path string) int {
	val := GetFieldValue(obj, path)
	if val == nil {
		return 0
	}
	if num, ok := val.(int); ok {
		return num
	}
	if num, ok := val.(float64); ok {
		return int(num)
	}
	return 0
}

// GetFieldMap is a helper to get field value as map.
func GetFieldMap(obj map[string]interface{}, path string) map[string]interface{} {
	val := GetFieldValue(obj, path)
	if val == nil {
		return nil
	}
	if m, ok := val.(map[string]interface{}); ok {
		return m
	}
	return nil
}

// EvaluateCondition checks if a condition is met.
func EvaluateCondition(cond *Condition, bc, build map[string]interface{}) bool {
	if cond == nil {
		return true
	}

	var obj map[string]interface{}
	if strings.HasPrefix(cond.Field, "bc.") {
		obj = bc
	} else {
		obj = build
	}

	actualValue := GetFieldString(obj, cond.Field)

	// Check equals
	if cond.Equals != "" {
		return actualValue == cond.Equals
	}

	// Check in_values
	if len(cond.InValues) > 0 {
		for _, v := range cond.InValues {
			if actualValue == v {
				return true
			}
		}
		return false
	}

	return true
}

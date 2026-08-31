package framework

import (
	"fmt"
	"strings"
)

// EvaluateRule evaluates a single rule against BC and Build.
// Returns a violation if the rule fails, nil if it passes.
func EvaluateRule(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	// Check if rule should be skipped (conditional presence)
	if rule.WhenBCFieldPresent != "" {
		val := GetFieldValue(bc, rule.WhenBCFieldPresent)
		if val == nil || val == "" {
			return nil // Skip this rule
		}
	}

	// Check when condition
	if !EvaluateCondition(rule.When, bc, build) {
		return nil // Condition not met, skip rule
	}

	// Dispatch to specific evaluator based on rule type
	switch rule.Type {
	case "field_equals":
		return evaluateFieldEquals(rule, bc, build)
	case "conditional_mapping":
		return evaluateConditionalMapping(rule, bc, build)
	case "annotation_pattern":
		return evaluateAnnotationPattern(rule, bc, build)
	case "annotation_exists":
		return evaluateAnnotationExists(rule, bc, build)
	case "field_mapping":
		return evaluateFieldMapping(rule, bc, build)
	case "labels_preserved":
		return evaluateLabelsPreserved(rule, bc, build)
	case "triggers_annotation":
		return evaluateTriggersAnnotation(rule, bc, build)
	case "field_absent":
		return evaluateFieldAbsent(rule, bc, build)
	case "conditional_annotation":
		return evaluateConditionalAnnotation(rule, bc, build)
	case "retention_limit":
		return evaluateRetentionLimit(rule, bc, build)
	case "timeout_mapping":
		return evaluateTimeoutMapping(rule, bc, build)
	default:
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    "known rule type",
			Actual:      fmt.Sprintf("unknown type: %s", rule.Type),
		}
	}
}

func evaluateFieldEquals(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	actual := GetFieldString(build, rule.Field)
	if actual != rule.Expected {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    rule.Expected,
			Actual:      actual,
		}
	}
	return nil
}

func evaluateConditionalMapping(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	if rule.Then == nil {
		return nil
	}

	var expected string
	if rule.Then.Expected != "" {
		expected = rule.Then.Expected
	} else if rule.Then.EqualsBCField != "" {
		expected = GetFieldString(bc, rule.Then.EqualsBCField)
	}

	actual := GetFieldString(build, rule.Then.Field)
	if actual != expected {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    expected,
			Actual:      actual,
		}
	}
	return nil
}

func evaluateAnnotationPattern(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	// Get annotation value directly from build map
	buildData := build["build"].(map[string]interface{})
	metadata := buildData["metadata"].(map[string]interface{})
	annotations, _ := metadata["annotations"].(map[string]interface{})
	actual, _ := annotations[rule.Annotation].(string)

	// Build expected pattern
	expected := rule.Pattern
	// Replace {bc.name} with actual BC name
	bcName := GetFieldString(bc, "bc.metadata.name")
	expected = strings.ReplaceAll(expected, "{bc.name}", bcName)

	if actual != expected {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    expected,
			Actual:      actual,
		}
	}
	return nil
}

func evaluateAnnotationExists(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	// Get annotation value directly from build map
	buildData := build["build"].(map[string]interface{})
	metadata, _ := buildData["metadata"].(map[string]interface{})
	annotations, _ := metadata["annotations"].(map[string]interface{})
	actual, _ := annotations[rule.Annotation].(string)

	if actual == "" {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    "annotation to exist",
			Actual:      "missing",
		}
	}

	// Check allowed values
	if len(rule.AllowedValues) > 0 {
		found := false
		for _, allowed := range rule.AllowedValues {
			if actual == allowed {
				found = true
				break
			}
		}
		if !found {
			return &RuleViolation{
				Rule:        rule.ID,
				Description: rule.Description,
				Expected:    strings.Join(rule.AllowedValues, " or "),
				Actual:      actual,
			}
		}
	}

	return nil
}

func evaluateFieldMapping(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	expected := GetFieldString(bc, rule.BCField)
	actual := GetFieldString(build, rule.BuildField)

	if actual != expected {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    expected,
			Actual:      actual,
		}
	}
	return nil
}

func evaluateLabelsPreserved(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	bcLabels := GetFieldMap(bc, rule.BCLabels)
	buildLabels := GetFieldMap(build, rule.BuildLabels)

	if bcLabels == nil {
		return nil // No labels to preserve
	}

	for key, bcValue := range bcLabels {
		buildValue, exists := buildLabels[key]
		if !exists {
			return &RuleViolation{
				Rule:        rule.ID,
				Description: fmt.Sprintf("Label %s must be preserved", key),
				Expected:    fmt.Sprintf("%v", bcValue),
				Actual:      "(missing)",
			}
		}
		if fmt.Sprintf("%v", buildValue) != fmt.Sprintf("%v", bcValue) {
			return &RuleViolation{
				Rule:        rule.ID,
				Description: fmt.Sprintf("Label %s value", key),
				Expected:    fmt.Sprintf("%v", bcValue),
				Actual:      fmt.Sprintf("%v", buildValue),
			}
		}
	}
	return nil
}

func evaluateTriggersAnnotation(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	triggers := GetFieldValue(bc, "bc.spec.triggers")
	if triggers == nil {
		return nil // No triggers to preserve
	}

	// Get annotation value directly from build map
	buildData := build["build"].(map[string]interface{})
	metadata, _ := buildData["metadata"].(map[string]interface{})
	annotations, _ := metadata["annotations"].(map[string]interface{})
	actual, _ := annotations[rule.Annotation].(string)

	if actual == "" {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    "triggers preserved in annotation",
			Actual:      "annotation missing",
		}
	}
	return nil
}

func evaluateFieldAbsent(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	val := GetFieldValue(build, rule.Field)
	if val != nil && val != "" {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    "field absent",
			Actual:      fmt.Sprintf("found: %v", val),
		}
	}
	return nil
}

func evaluateConditionalAnnotation(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	if rule.Then == nil {
		return nil
	}

	// Get annotation value directly from build map
	buildData := build["build"].(map[string]interface{})
	metadata, _ := buildData["metadata"].(map[string]interface{})
	annotations, _ := metadata["annotations"].(map[string]interface{})
	actual, _ := annotations[rule.Then.Annotation].(string)

	if actual != rule.Then.Expected {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    rule.Then.Expected,
			Actual:      actual,
		}
	}
	return nil
}

func evaluateRetentionLimit(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	bcValue := GetFieldInt(bc, rule.BCField)
	if bcValue == 0 {
		return nil // Not set in BC
	}

	buildValue := GetFieldInt(build, rule.BuildField)

	// Check if value is in valid range
	if bcValue < rule.Min || bcValue > rule.Max {
		// Out of range - should be dropped
		if buildValue != 0 {
			return &RuleViolation{
				Rule:        rule.ID,
				Description: fmt.Sprintf("%s (out of range %d-%d should be dropped)", rule.Description, rule.Min, rule.Max),
				Expected:    "not set",
				Actual:      fmt.Sprintf("%d", buildValue),
			}
		}
		return nil
	}

	// In range - should be preserved
	if buildValue != bcValue {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    fmt.Sprintf("%d", bcValue),
			Actual:      fmt.Sprintf("%d", buildValue),
		}
	}
	return nil
}

func evaluateTimeoutMapping(rule Rule, bc, build map[string]interface{}) *RuleViolation {
	bcValue := GetFieldInt(bc, rule.BCField)
	if bcValue == 0 {
		return nil // Not set in BC
	}

	// Build timeout is in Go duration format (e.g., "3600s")
	buildDuration := GetFieldString(build, rule.BuildField)
	if buildDuration == "" {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    fmt.Sprintf("%ds", bcValue),
			Actual:      "(not set)",
		}
	}

	// Expected format: "<seconds>s"
	expected := fmt.Sprintf("%ds", bcValue)
	if buildDuration != expected {
		return &RuleViolation{
			Rule:        rule.ID,
			Description: rule.Description,
			Expected:    expected,
			Actual:      buildDuration,
		}
	}
	return nil
}

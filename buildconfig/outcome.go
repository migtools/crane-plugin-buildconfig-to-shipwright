package buildconfig

import "fmt"

// OutcomeState is the disposition of a single BuildConfig conversion. Every
// BuildConfig ends in exactly one state (BUILD-2318).
type OutcomeState string

const (
	// OutcomeConverted: a Shipwright Build was generated with no warnings.
	OutcomeConverted OutcomeState = "converted"
	// OutcomeConvertedWithWarnings: a Build was generated, but something was
	// dropped or needs review — the warnings are on the Outcome and in the logs.
	OutcomeConvertedWithWarnings OutcomeState = "converted-with-warnings"
	// OutcomeSkipped: the BuildConfig was intentionally not converted (e.g. an
	// unsupported strategy or a missing output image). It is passed through
	// unchanged.
	OutcomeSkipped OutcomeState = "skipped"
	// OutcomeFailed: conversion hit an error. The BuildConfig is passed through
	// unchanged so the rest of the migration can continue (crane aborts the
	// whole run on any plugin error, so the plugin never returns one for a
	// single-BuildConfig failure).
	OutcomeFailed OutcomeState = "failed"
)

// Outcome describes how one BuildConfig conversion ended.
type Outcome struct {
	State  OutcomeState
	Reason string // why it was skipped or failed; empty when converted
	// Warnings holds every conversion warning recorded while producing this
	// Build, so a converted-with-warnings Outcome is self-describing and a
	// caller (e.g. the BUILD-2319 report) need not re-parse logs. Empty for a
	// clean conversion; not populated for skipped/failed outcomes.
	Warnings []string
}

// outcomeConverted is a convenience for a successful conversion; State is set to
// converted-with-warnings later if any warning was recorded.
func outcomeConverted() Outcome            { return Outcome{State: OutcomeConverted} }
func outcomeSkipped(reason string) Outcome { return Outcome{State: OutcomeSkipped, Reason: reason} }
func outcomeFailed(reason string) Outcome  { return Outcome{State: OutcomeFailed, Reason: reason} }

// warnf is the one way to record a conversion warning: it appends the message to
// c.warnings (the single source that drives converted-with-warnings, the
// ConversionWarningsAnnotation, and Outcome.Warnings) and logs it at WARN. All
// field-drop and degraded-conversion messages must go through warnf; a message
// logged directly via c.Log would not be counted and would misclassify the
// outcome (the one deliberate exception is the name-collision error in
// uniqueName, which records into c.warnings explicitly alongside a louder ERROR).
func (c *Converter) warnf(format string, args ...interface{}) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
	c.Log.Warnf(format, args...)
}

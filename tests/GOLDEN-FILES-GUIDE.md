# Golden File Comparison Guide

## What Was Done

Reorganized test data and added golden file comparison to provide two-level validation.

## New Structure

```
tests/testdata/
├── buildconfig_yamls/      # 20 BuildConfig test inputs
│   ├── 01-datagrid-hotrod.yaml
│   ├── 02-cakephp-mysql.yaml
│   ├── ...
│   └── 20-s2i-imagestream-nodejs.yaml
│
└── expected_output/        # 10 expected Build outputs (golden files)
    ├── 03-docker-and-s2i-expected.yaml
    ├── 04-webapp-docker-expected.yaml
    ├── 05-api-s2i-expected.yaml
    ├── 08-docker-with-envvars-expected.yaml
    ├── 09-s2i-with-envvars-expected.yaml
    ├── 10-docker-with-volumes-expected.yaml
    ├── 17-docker-nocache-expected.yaml
    ├── 18-serviceaccount-override-expected.yaml
    ├── 19-docker-imagestream-ruby-expected.yaml
    └── 20-s2i-imagestream-nodejs-expected.yaml
```

## Two-Level Validation

### Level 1: Rule-Based (All 20 tests)
Every test validates against 13 declarative rules in `rules.yaml`:
- API version correctness
- Strategy mapping
- Annotations presence
- Field preservation
- etc.

**Pros:**
- Fast
- Clear error messages (which rule failed)
- Flexible (allows plugin to add new fields)

### Level 2: Golden File Comparison (10 tests)
Tests with expected outputs also compare complete YAML:
- Exact field-by-field comparison
- Catches unexpected changes
- Complete validation

**Pros:**
- Comprehensive
- Catches fields not covered by rules
- Visual diff on mismatch

## How It Works

```go
// For test: 04-webapp-docker.yaml

// 1. Run plugin
builds := RunPluginOnYAML("buildconfig_yamls/04-webapp-docker.yaml")

// 2. Validate against rules
violations := ValidateConversion(buildConfig, build)
// ✓ All 13 rules pass

// 3. Compare with golden file (if exists)
expectedPath := "expected_output/04-webapp-docker-expected.yaml"
diffs := CompareWithGoldenFile(build, expectedPath)
// ✓ Exact match
```

## Generated Files

**10 expected outputs** were auto-generated from actual plugin execution:

```
Processing: 03-docker-and-s2i.yaml         ✓ Generated
Processing: 04-webapp-docker.yaml          ✓ Generated
Processing: 05-api-s2i.yaml                ✓ Generated
Processing: 08-docker-with-envvars.yaml    ✓ Generated
Processing: 09-s2i-with-envvars.yaml       ✓ Generated
Processing: 10-docker-with-volumes.yaml    ✓ Generated
Processing: 17-docker-nocache.yaml         ✓ Generated
Processing: 18-serviceaccount-override.yaml ✓ Generated
Processing: 19-docker-imagestream-ruby.yaml ✓ Generated
Processing: 20-s2i-imagestream-nodejs.yaml  ✓ Generated
```

**10 tests skipped** (no golden file):
- 2 are Templates (need preprocessing)
- 2 are skipped strategies (JenkinsPipeline, Custom)
- 6 have incomplete BuildConfigs or multi-doc issues

## Generator Tool

Location: `.tools/generate-expected/`

**To regenerate expected outputs:**
```bash
cd .tools/generate-expected
go run main.go
```

**How it works:**
1. Reads each BuildConfig from `buildconfig_yamls/`
2. Runs it through the plugin
3. Saves the output to `expected_output/`

**When to regenerate:**
- Plugin conversion logic changes
- Adding new test cases
- Updating expected behavior

## Adding New Golden Files

### Option 1: Automatic (Recommended)
```bash
# Add your BuildConfig
cp my-test.yaml tests/testdata/buildconfig_yamls/21-my-test.yaml

# Regenerate all
cd .tools/generate-expected
go run main.go

# Will create: expected_output/21-my-test-expected.yaml
```

### Option 2: Manual
```bash
# Run plugin on your BuildConfig
cd tests
go run ../main.go < testdata/buildconfig_yamls/21-my-test.yaml > /tmp/output.yaml

# Review output
cat /tmp/output.yaml

# If correct, save as golden file
cp /tmp/output.yaml testdata/expected_output/21-my-test-expected.yaml
```

## Updating Existing Golden Files

When plugin behavior changes:

```bash
# Regenerate all golden files
cd .tools/generate-expected
go run main.go

# Review changes
cd ../..
git diff tests/testdata/expected_output/

# If changes are correct (intended behavior change)
git add tests/testdata/expected_output/
git commit -m "Update expected outputs for [reason]"

# If changes are wrong (bug in plugin)
# Fix the plugin, then regenerate
```

## Test Behavior

**Test with golden file:**
```
Input: buildconfig_yamls/04-webapp-docker.yaml
  ↓
Run plugin
  ↓
Validate rules (13 checks) → ✓ Pass
  ↓
Compare with golden file → ✓ Exact match
  ↓
✓ Test passes
```

**Test without golden file:**
```
Input: buildconfig_yamls/06-jenkins-pipeline.yaml
  ↓
Run plugin
  ↓
Validate rules (13 checks) → ✓ Pass (correctly skipped)
  ↓
No golden file → Skip comparison
  ↓
✓ Test passes
```

## Benefits

### Before (Rule-Only)
- ✓ Fast
- ✓ Clear failures
- ❌ Only validates what we thought of
- ❌ Might miss unexpected changes

### After (Rules + Golden Files)
- ✓ Fast (rules)
- ✓ Clear failures (rules)
- ✓ Complete validation (golden files)
- ✓ Catches unexpected changes (golden files)

## Current Test Status

**With golden files (10 tests):**
- Complete two-level validation
- Exact YAML comparison
- High confidence

**Without golden files (10 tests):**
- Rule-based validation only
- Still valuable
- Can add golden files later

## Maintenance

### When Plugin Changes

**Minor change** (new annotation, optimization):
- Regenerate golden files
- Commit updated expectations

**Major change** (new strategy, field rework):
- Update rules.yaml first
- Regenerate golden files
- Review all diffs carefully

### When Adding Tests

**Simple test:**
- Add BuildConfig YAML
- Regenerate (automatic golden file)

**Complex test:**
- Add BuildConfig YAML
- Manually review generated output
- Adjust if needed
- Save as golden file

## Summary

**What:** Reorganized test data into inputs/outputs, added golden file comparison  
**Why:** Two-level validation (rules + exact YAML) for higher confidence  
**How:** Generator tool auto-creates expected outputs from plugin execution  
**Benefit:** Catches both known issues (rules) and unknown issues (golden files)

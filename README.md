# crane-plugin-buildconfig-to-shipwright

A [crane](https://github.com/konveyor/crane) transform plugin that converts OpenShift `BuildConfig` resources (`build.openshift.io/v1`) to [Shipwright](https://shipwright.io/) `Build` CRs (`shipwright.io/v1beta1`).

1. [What it does](#what-it-does)
2. [Strategy support](#strategy-support)
3. [Plugin flags](#plugin-flags)
4. [Usage with crane](#usage-with-crane)
   - [1. Export the namespace](#1-export-the-namespace)
   - [2. Transform with plugins](#2-transform-with-plugins)
   - [3. Review the output](#3-review-the-output)
   - [4. Apply to the target cluster](#4-apply-to-the-target-cluster)
   - [Full example](#full-example)
5. [Conversion example](#conversion-example)
6. [Building](#building)
7. [Testing](#testing)
8. [Known limitations](#known-limitations)
9. [Issue tracking](#issue-tracking)
10. [Development skills](#development-skills)
    - [Workflow](#workflow)
    - [The skills](#the-skills)
    - [Getting started](#getting-started)
    - [Two review skills, two scopes](#two-review-skills-two-scopes)
    - [Walkthrough](#walkthrough)
11. [Related](#related)

## What it does

During crane's transform phase, this plugin:

1. Detects `BuildConfig` resources in the exported namespace
2. Whiteouts the original BuildConfig (marks it for deletion)
3. Generates a corresponding Shipwright `Build` resource
4. Optionally generates a `ServiceAccount` when pull secrets are referenced

All other resource types are passed through unchanged.

## Strategy support

| BuildConfig Strategy | Shipwright ClusterBuildStrategy | Status |
|---------------------|-------------------------------|--------|
| Docker | `buildah` | Supported |
| Source (S2I) | `source-to-image` | Supported |
| Custom | — | Error (no equivalent) |
| JenkinsPipeline | — | Error (migrate to Tekton) |

## Plugin flags

| Flag | Format | Purpose |
|------|--------|---------|
| `registry-mapping` | `old=new,old2=new2` | Rewrite image registry references |
| `imagestream-mapping` | `ns/name:tag=registry/image:tag` | Resolve ImageStreamTag references to concrete image URLs |
| `default-build-strategy` | `docker=my-buildah,s2i=my-s2i` | Override default ClusterBuildStrategy names |
| `search-registries` | `reg1,reg2` | Search registries for Buildah |
| `insecure-registries` | `reg1,reg2` | Insecure registries for Buildah |
| `block-registries` | `reg1,reg2` | Blocked registries for Buildah |

## Usage with crane

### 1. Export the namespace

```bash
crane export -n myapp --export-dir ./migration
```

This exports all resources including BuildConfigs, ImageStreams, etc.

### 2. Transform with plugins

```bash
crane transform \
  --export-dir ./migration \
  --transform-dir ./migration/transform \
  --plugin-dir ./plugins
```

The plugin directory should contain the `crane-plugin-buildconfig-to-shipwright` binary. Crane calls it for each resource automatically.

To pass plugin flags, use the `--optional-flags` parameter:

```bash
crane transform \
  --export-dir ./migration \
  --transform-dir ./migration/transform \
  --plugin-dir ./plugins \
  --optional-flags "registry-mapping=image-registry.openshift-image-registry.svc:5000=quay.io/myorg,imagestream-mapping=myns/mybuilder:latest=quay.io/myorg/builder:latest"
```

### 3. Review the output

After transform, the output directory contains:

```
migration/transform/
  resources/
    BuildConfig_build.openshift.io_v1_myapp_myapp-build.yaml  # whiteout
    Build_shipwright.io_v1beta1_myapp_myapp-build.yaml         # new Shipwright Build
    ServiceAccount_v1_myapp_myapp-build.yaml                   # if pull secrets used
  ...
```

Review the generated Shipwright Build YAMLs before applying.

### 4. Apply to the target cluster

```bash
crane apply \
  --transform-dir ./migration/transform \
  --output-dir ./migration/output

kubectl apply -f ./migration/output/resources/
```

### Full example

Migrating a namespace with a Dockerfile-based BuildConfig from OpenShift to a Shipwright-enabled cluster:

```bash
# Export from source cluster
crane export -n myapp --export-dir ./migration

# Transform — OpenShift plugin strips OCP-specific resources,
# BuildConfig plugin converts builds to Shipwright
crane transform \
  --export-dir ./migration \
  --transform-dir ./migration/transform \
  --plugin-dir ./plugins \
  --optional-flags "registry-mapping=image-registry.openshift-image-registry.svc:5000=quay.io/myorg"

# Review generated Shipwright Builds
cat ./migration/transform/resources/Build_shipwright.io_v1beta1_myapp_*.yaml

# Apply to target cluster (Shipwright + Tekton must be installed)
crane apply \
  --transform-dir ./migration/transform \
  --output-dir ./migration/output

kubectl apply -f ./migration/output/resources/
```

## Conversion example

**Input — OpenShift BuildConfig:**

```yaml
apiVersion: build.openshift.io/v1
kind: BuildConfig
metadata:
  name: myapp-build
  namespace: myapp
spec:
  source:
    type: Git
    git:
      uri: https://github.com/example/myapp.git
      ref: main
    contextDir: src
    sourceSecret:
      name: git-credentials
  strategy:
    type: Docker
    dockerStrategy:
      dockerfilePath: Dockerfile.prod
      buildArgs:
        - name: GO_VERSION
          value: "1.21"
  output:
    to:
      kind: DockerImage
      name: quay.io/example/myapp:latest
    pushSecret:
      name: quay-push-secret
```

**Output — Shipwright Build:**

```yaml
apiVersion: shipwright.io/v1beta1
kind: Build
metadata:
  name: myapp-build
  namespace: myapp
  annotations:
    crane.konveyor.io/converted-from: build.openshift.io/v1/BuildConfig/myapp-build
spec:
  source:
    type: Git
    git:
      url: https://github.com/example/myapp.git
      revision: main
      cloneSecret: git-credentials
    contextDir: src
  strategy:
    name: buildah
    kind: ClusterBuildStrategy
  paramValues:
    - name: dockerfile
      value: Dockerfile.prod
    - name: build-args
      values:
        - value: "GO_VERSION=1.21"
  output:
    image: quay.io/example/myapp:latest
    pushSecret: quay-push-secret
```

## Building

```bash
GOTOOLCHAIN=auto go build -o crane-plugin-buildconfig-to-shipwright .
```

Requires Go 1.26+ (forced by transitive dependencies).

## Testing

```bash
GOTOOLCHAIN=auto go test ./...
```

## Known limitations

- **No live cluster access** — ImageStream references must be resolved via `--imagestream-mapping` or `--registry-mapping` flags. Without them, the plugin falls back to the internal OpenShift registry URL with a warning.
- **Volumes** — BuildConfig volumes are not converted (Shipwright requires BuildStrategy-level support). A warning is emitted.
- **Inline Dockerfiles** — Not supported for Docker strategy; must be in a separate file.
- **Multiple source types** — Shipwright supports one source per Build. BuildConfigs with multiple sources produce an error.
- **BuildRun not generated** — Only the Build definition is created. Triggering builds is left to the user or CI/CD system.

## Issue tracking

This project is tracked primarily in Jira, under the BUILD project. This is different from
crane, which is tracked primarily in GitHub (Projects and Issues) and uses Jira only for the
non-upstream tracking that is required internally. If you are picking up or filing work for
this plugin, use Jira as the source of truth.

## Development skills

This repo ships six [Claude Code](https://claude.com/claude-code) skills under
`.claude/skills/`. Together they automate the path from a Jira BUILD issue to a reviewed
pull request: research and triage, implementation, unit and cluster testing, a pre-PR
review gate, and multi-agent review of the published PR.

Each one is invoked as a slash command from inside a clone of this repo. Every skill's
full instructions live in its own `SKILL.md`. This section is the map, not the manual.

They are development tooling only. Nothing here is needed to *use* the plugin; if you are
migrating BuildConfigs, [Usage with crane](#usage-with-crane) is the section you want.

### Workflow

```
  Setup    ┌────────────────────────────────────────────┐
           │  /setup-repos                              │  ◄── run once per machine
           │  finds your local clones, writes their     │
           │  paths to repo.md. Every skill reads it    │
           └─────────────────────┬──────────────────────┘
                                 │
  ═════════════════════════════════════════════════════════════
   Everything below runs once per Jira issue
  ═════════════════════════════════════════════════════════════
                                 │
                                 ▼
  Phase 1  ┌────────────────────────────────────────────┐
           │  /tech-design BUILD-XXXX                   │  ◄── priority comes last, and
           │  should this be built at all? Checks       │      only with a file:line
           │  upstream, shipped code and open PRs       │      behind every claim
           │  → design doc, once you approve it         │
           └─────────────────────┬──────────────────────┘
                                 │
  ═════════════════════════════════════════════════════════════
   DECISION: is the change needed, and is it unblocked?
     No  → record the finding in Jira, close it, done
     Yes → continue below
  ═════════════════════════════════════════════════════════════
                                 │
                                 ▼
  Phase 2  ┌────────────────────────────────────────────┐
           │  /tech-implement BUILD-XXXX                │  ◄── refuses to start
           │  turns the design doc into code, in its    │      without a design doc
           │  own worktree so a shared clone is safe    │
           │  catalog first, then converter, then tests │
           └─────────────────────┬──────────────────────┘
                                 │
                                 ▼
  Phase 3  ┌────────────────────────────────────────────┐
           │  /tech-test BUILD-XXXX unit                │  ◄── no cluster — runs on
           │  compiles the branch, runs the Go suite    │      any clone
           │  and the offline conversion checks         │
           └─────────────────────┬──────────────────────┘
                                 │
                                 ▼
  Phase 4  ┌────────────────────────────────────────────┐
           │  /tech-review BUILD-XXXX                   │  ◄── plus five checks a
           │  the gate before a branch becomes a PR:    │      general reviewer
           │  reviewers in parallel, then an agent      │      does not perform
           │  paid to disprove every blocker            │
           └─────────────────────┬──────────────────────┘
                                 │
                                 ▼
  Phase 5  ┌────────────────────────────────────────────┐
           │  /tech-test BUILD-XXXX cluster             │  ◄── needs a real cluster.
           │  runs the original BuildConfig first,      │      Succeeding is not the
           │  then the converted one, and compares      │      same as doing the same job
           │  the output images by digest and labels    │
           └─────────────────────┬──────────────────────┘
                                 │
                                 ▼
  Phase 6  ┌────────────────────────────────────────────┐
           │  open the PR against migtools/             │
           │  crane-plugin-buildconfig-to-shipwright    │
           └─────────────────────┬──────────────────────┘
                                 │
                                 ▼
  Phase 7  ┌────────────────────────────────────────────┐
           │  /deep-review <PR#>                        │  ◄── open PRs only — yours
           │  six reviewers with non-overlapping        │      or anyone else's.
           │  beats, then a challenger that can only    │      Silence counts as a finding
           │  delete findings, never add them           │
           └────────────────────────────────────────────┘

  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─

  Anytime:
    /setup-repos update   — re-scan after cloning a new repo
    /deep-review <PR#>    — review any open PR, no local branch needed
```

Each phase is its own command. Review sits between the two test stages on purpose: the
unit stage is cheap and catches what review should not waste time on, while the cluster
stage is slow enough that repeating it after review-driven changes is the largest
avoidable cost in the loop.

### The skills

| Command | What it does | Needs first | Leaves behind |
|---------|--------------|-------------|---------------|
| `/setup-repos [update]` | Finds your local clones of the repos this work touches and writes their paths to `repo.md`. Every other skill reads that file, so no skill hardcodes a path that only exists on your machine | — | `repo.md` at the project root |
| `/tech-design <ISSUE-KEY>` | Works out whether an issue should be built at all, before working out how. Checks whether the feature already exists upstream, already shipped, or is sitting in someone's open PR. Then it asks whether Shipwright's design makes it unnecessary anyway. Priority and story points come last, and every claim has to cite a file and line | `repo.md` | A design doc under `designs/`, plus a Jira comment. Both are written only after you approve them |
| `/tech-implement <ISSUE-KEY>` | Turns the approved design doc into code, and refuses to start without one. Works in its own throwaway worktree rather than your checkout, because two sessions sharing a clone share one index and one HEAD. Strategy catalog changes go first, then the converter, then the tests. A conversion cannot be tested against a strategy parameter that does not exist yet | A design doc | A branch on your fork; test results under `designs/test-results/` |
| `/tech-test <ISSUE-KEY> unit` | Compiles the branch and runs the Go suite plus the offline conversion checks. No cluster, so it runs on any clone. It goes before review, because reviewing code that does not compile wastes the reviewer | A branch | A run report |
| `/tech-review [<ISSUE-KEY>] [--fix]` | The gate a branch passes before it becomes a PR. Runs general reviewers in parallel, then hands every blocking finding to a separate agent whose only job is to disprove it. A false blocker stops a good branch, and that is the expensive failure. Then adds five checks no general reviewer performs; the sharpest compares the parameter names the converter emits against those the strategy YAML actually defines, since a mismatch compiles cleanly and only fails on the cluster with `UndefinedParameter` | A branch whose unit tests pass | Findings in the terminal. Commits nothing |
| `/tech-test <ISSUE-KEY> cluster` | Runs the original BuildConfig on a real cluster first, then the converted Build, and compares the two output images by digest and labels. A converted Build that merely succeeds proves nothing. The question is whether it did the same job as the one it replaced. That comparison is what makes it a test rather than a smoke check | A reviewed branch, `oc`, and a cluster | A run report; fixtures archived, then only what it created is deleted |
| `/deep-review <pr-number\|url>` | Six reviewers read an open PR in parallel, each with an explicit list of what it does and does not own, so they do not all report the same naming nit. A final challenger reads the findings and the diff, but never the orchestrator's reasoning, and can only delete findings, never add them. If a top-tier reviewer returns nothing, that silence is recorded as a finding rather than passing as a clean bill of health | An open PR | Findings in the terminal. Posts nothing unless asked |

### Getting started

You need [Claude Code](https://claude.com/claude-code), `gh` authenticated against GitHub,
and `jira-cli` configured. `/tech-design` checks `jira me` before it does anything else.
The `/tech-test` cluster stage also needs `oc` and a reachable OpenShift cluster.

Then, once per machine:

```
/setup-repos
```

It scans your work directory for the clones the other skills read and writes their paths
to `repo.md`. Those paths differ per machine, so `repo.md` is gitignored and never
committed; `.claude/skills/setup-repos/repo_example.md` is the template it follows. Run
`/setup-repos update` after cloning a new repo rather than editing the file by hand.

`designs/` is gitignored for the same reason. Design docs and test results are working
notes, not deliverables.

### Two review skills, two scopes

`/tech-review` and `/deep-review` sound alike. They do not overlap, and neither calls the
other.

|  | `/tech-review` | `/deep-review` |
|--|----------------|----------------|
| **When** | Before the PR exists, on a local branch | On an open PR |
| **Scope** | The branch and its paired strategy change | The PR as published |
| **Unique value** | Cross-repo consistency, test evidence | Adversarial multi-agent depth |
| **Reviews others' work** | No | Yes |

Neither checks out your branch or writes to Jira, and both are report-only by default.

`/deep-review` is not original work: its review logic is vendored verbatim from the
[fullsend](https://github.com/fullsend-ai/fullsend) agent bundle under Apache-2.0. See
[`.claude/skills/deep-review/README.md`](.claude/skills/deep-review/README.md) for the
attribution, the pinned upstream commit, and the local adaptations.

### Walkthrough

Taking one issue from triage to a reviewed pull request:

```
/setup-repos                     # once per machine, writes repo.md

/tech-design BUILD-2269          # research → designs/BUILD-2269-*.md
                                 # stop here if the issue turns out to be
                                 # unnecessary, already done, or blocked

/tech-implement BUILD-2269       # branch, code, commit, push to your fork

/tech-test BUILD-2269 unit       # compile gate + Go suite, no cluster
/tech-review BUILD-2269          # reviewers, challenger, cross-repo checks
/tech-test BUILD-2269 cluster    # real OpenShift, baseline vs converted
```

Then open the PR against upstream and review it as published:

```bash
gh pr create --repo migtools/crane-plugin-buildconfig-to-shipwright \
  --head <your-fork-owner>:BUILD-2269-sa-warning --base main
```

```
/deep-review 32                  # multi-agent review of the published PR
```

Push branches to your fork, never to `origin`. Upstream changes land through pull
requests only.

## Related

- [Enhancement proposal](https://github.com/konveyor/enhancements/pull/300)
- [crane-plugin-openshift](https://github.com/migtools/crane-plugin-openshift) — reference crane transform plugin
- [Shipwright documentation](https://shipwright.io/docs/)

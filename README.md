# crane-plugin-buildconfig-to-shipwright

A [crane](https://github.com/migtools/crane) transform plugin that converts OpenShift
`BuildConfig` resources (`build.openshift.io/v1`) into Shipwright `Build` resources
(`shipwright.io/v1beta1`). It runs offline, as part of `crane transform`, and never talks to
a cluster.

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

For every resource in a crane export:

- Anything that is not a BuildConfig passes through untouched.
- A BuildConfig with a Docker or Source strategy and an output image becomes a Shipwright
  `Build`. The original is removed from the output. When the BuildConfig has a pull secret
  and names no ServiceAccount, the plugin also generates a `ServiceAccount` carrying it. A
  BuildConfig that does name a ServiceAccount keeps it: crane migrates that account
  unchanged and the plugin never overwrites it, warning instead with the `oc secrets link`
  command that attaches the pull secret on the target. An
  inline Dockerfile on a Docker strategy is preserved in a `ConfigMap`, pointed at by the
  Build's `buildconfig-to-shipwright/inline-dockerfile-configmap` annotation; commit it to
  the source repository before running the Build. On a Source strategy an inline Dockerfile
  is dropped with a warning, because S2I does not use one.
- A BuildConfig with a Custom or JenkinsPipeline strategy, or no output image, is skipped:
  it stays in the output unchanged with two annotations saying it was skipped and why. A
  BuildConfig the plugin cannot convert is treated the same way, marked failed. Shipwright
  takes one source per Build, so a BuildConfig with more than one source type fails here.
  Neither stops the migration.

Every field the plugin drops or changes produces a warning, in the log and in an annotation
on the Build. The annotation is size-capped, so on a very lossy BuildConfig the log is the
complete list. The full list, field by field, is in [docs/support-matrix.md](docs/support-matrix.md).

| BuildConfig strategy | Shipwright ClusterBuildStrategy | Outcome |
|---|---|---|
| Docker | `buildah` | converted |
| Source (S2I) | `source-to-image` | converted |
| Custom | none | skipped, passed through with two annotations |
| JenkinsPipeline | none | skipped, passed through with two annotations |

## Prerequisites

- **Go 1.25.6 or newer** to build the plugin.
- **crane built from commit `d566a18f6640cd79c8568749d6621b40486d0625` or newer.** The
  released crane (v0.0.5) does not write the resources a plugin generates: it runs this
  plugin, reports nothing, and produces no Builds. This is the commit CI pins.
- **A target cluster with Shipwright and Tekton**, and the `buildah` and `source-to-image`
  ClusterBuildStrategies. Builds for Red Hat OpenShift ships both. Upstream, CI tests
  against Shipwright v0.19.0.

### Install crane

```bash
git clone https://github.com/migtools/crane.git
cd crane
git checkout d566a18f6640cd79c8568749d6621b40486d0625
go build -o crane .
sudo mv crane /usr/local/bin/
crane version
```

### Build the plugin

```bash
GOTOOLCHAIN=auto go build -o crane-plugin-buildconfig-to-shipwright .
mkdir -p plugins && mv crane-plugin-buildconfig-to-shipwright plugins/
```

crane finds plugins by scanning the directory passed as `--plugin-dir`.

## Usage with crane

### 1. Export the namespace

```bash
crane export -n myapp
```

### 2. Transform

```bash
crane transform BuildConfigPlugin \
  --plugin-dir ./plugins \
  --optional-flags '{"registry-mapping":"image-registry.openshift-image-registry.svc:5000=quay.io/myorg"}'
```

`--optional-flags` takes one JSON object whose keys are the plugin's flags and whose values
are strings. The flags are listed [below](#plugin-flags); `crane transform optionals
--plugin-dir ./plugins` prints them with an example each.

### 3. Write the output, then read it

```bash
crane apply
```

`crane apply` writes the result under `output/`:

```
output/
  output.yaml                                   # everything, concatenated
  resources/myapp/
    Build_shipwright.io_v1beta1_myapp_webapp.yaml
    ServiceAccount__v1_myapp_webapp.yaml         # when a pull secret is used and no ServiceAccount is named
    ConfigMap__v1_myapp_webapp-dockerfile.yaml   # when the BuildConfig has an inline Dockerfile
```

Read each Build's `crane.konveyor.io/conversion-warnings` annotation before applying it.
To find the BuildConfigs that were not converted, look for the outcome annotation on the
objects that still say `kind: BuildConfig`:

```bash
grep -rl 'kind: BuildConfig' output/resources \
  | xargs -r grep -H 'buildconfig-to-shipwright/conversion-'
```

The export also carries the resources the plugin left alone, including the BuildConfigs it
skipped. Read those before step 4: a recreated BuildConfig with an ImageChange or
ConfigChange trigger starts an OpenShift build as soon as it lands.

### 4. Apply to the target cluster

`crane` writes each resource under `output/resources/<namespace>/`, so the apply has to
recurse:

```bash
kubectl apply -R -f output/resources/

kubectl wait --for=jsonpath='{.status.registered}'=True \
  build.shipwright.io/webapp -n myapp --timeout=120s
```

Write `build.shipwright.io`, not `build`, in every kubectl command. On OpenShift the short
name resolves to the OpenShift Build API.

Nothing builds on its own. OpenShift triggers do not exist in Shipwright, so create a
`BuildRun` to start the first build.

Which ServiceAccount it runs as depends on whether the plugin generated one. If it did not,
leave the BuildRun's `serviceAccount` unset and it runs as the namespace `pipeline` account.
If it did, that account carries the BuildConfig's pull secret and the plugin names it in the
Build's `buildconfig-to-shipwright/buildrun-template` annotation, so point the BuildRun at
it. Leaving it unset there drops the pull secret and a private builder image will not pull.
On OpenShift, grant the generated account the SCC buildah needs, scoped to that one account:

```bash
oc adm policy add-scc-to-user pipelines-scc -z <generated-sa> -n <namespace>
```

## Worked examples

[docs/examples](docs/examples/README.md) holds three BuildConfigs taken through the plugin,
with the exact input, the flags, the output, every warning, and the steps on the target
cluster. A test regenerates their output on every CI run, so they cannot drift.

## Plugin flags

| Flag | Format | What it changes |
|---|---|---|
| `registry-mapping` | `old-registry=new-registry,…` | Rewrites the registry prefix of resolved image references. Applies to strategy and source images, and to an output of kind `ImageStreamTag`. An output of kind `DockerImage` is copied as written |
| `imagestream-mapping` | `ns/name:tag=registry/image:tag,…` | Replaces an ImageStreamTag or ImageStreamImage reference, or a bare DockerImage name that relied on `lookupPolicy.local`, with a concrete image. Digest form: `ns/name@sha256:…=…` |
| `default-build-strategy` | `docker=name,s2i=name` | Uses a different ClusterBuildStrategy name |
| `search-registries` | `registry,…` | Buildah search registries |
| `insecure-registries` | `registry,…` | Docker strategy: the `registries-insecure` param. Source strategy: `spec.output.insecure: true` when the output image is on one of them, because Shipwright does the push there |
| `block-registries` | `registry,…` | Buildah blocked registries |

### Redirecting output images

A BuildConfig pushes to the internal OpenShift registry. On the target cluster that registry
may not exist, so the two mapping flags redirect the output. There is no single
`--dest-registry` flag.

- `registry-mapping` rewrites the prefix and keeps the `<namespace>/<name>` path. Mapping the
  internal registry to `quay.io/acme` turns an ImageStreamTag output in namespace `myapp` into
  `quay.io/acme/myapp/webapp:latest`. Quay accepts nested paths like that and creates the
  repository on first push. Docker Hub does not; for registries that take only
  `<org>/<repo>`, name an exact target per BuildConfig with `imagestream-mapping`.
  `registry-mapping` still runs afterwards on the mapped value.
- Without either flag, an ImageStreamTag output keeps its internal-registry form,
  `image-registry.openshift-image-registry.svc:5000/<namespace>/<name>:<tag>`, with a warning.
  That is right when the target is another OpenShift cluster.
- Redirecting an output off the internal registry means the source ImageStream no longer
  updates, so a Deployment or DeploymentConfig that rolled out on it stops firing. The plugin
  warns when this happens. The check is a registry-prefix comparison, not a cluster-aware one.

The plugin cannot read the builder ServiceAccount to work out a push credential. Set
`output.pushSecret` on the BuildConfig, or make sure the BuildRun's ServiceAccount can push
to the target registry. The plugin warns either way.

## Documentation

| Page | For |
|---|---|
| [docs/support-matrix.md](docs/support-matrix.md) | every BuildConfig field: what happens, where it lands, what to do by hand, the warning |
| [docs/examples](docs/examples/README.md) | three worked examples, verified on a cluster |
| [docs/volume-migration.md](docs/volume-migration.md) | why a Build with volumes fails with `UndefinedVolume`, and the strategy-copy fix |
| [docs/architecture.md](docs/architecture.md) | for maintainers and agents: how the plugin runs, the conversion steps, the rules that must stay true |
| [hack/README.md](hack/README.md) | setting up a Minikube cluster with Shipwright for the cluster tests |

## Testing

Three levels.

**Unit tests**, no cluster:

```bash
GOTOOLCHAIN=auto go test ./...
```

These include the tests that keep the documentation honest: the support matrix must list
every warning the code can emit, the architecture page must name every file and step, the
examples must match the plugin's output, and this README's flag examples and version numbers
must match the code and CI.

**Plugin E2E**, the binary driven by crane over sample exports, no cluster. Needs the pinned
crane first on `PATH`:

```bash
./tests/e2e-transform.sh
```

**Cluster E2E**, on a Minikube cluster with Tekton and Shipwright. Converts two BuildConfigs
through crane, diffs each Build against a committed golden file, applies it, and runs a
BuildRun to completion:

```bash
./tests/e2e-cluster.sh              # after ./hack/setup-minikube-shipwright.sh and ./hack/fake-minikube-buildconfig.sh
./tests/e2e-cluster.sh --skip-build # verify the manifests only
```

Pull requests run the unit tests and the cluster E2E.

## Issue tracking

Work on this plugin is tracked in Jira, project BUILD. crane itself is tracked on GitHub.
File and pick up work in Jira.

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
- [crane-plugin-openshift](https://github.com/migtools/crane-plugin-openshift), the reference crane transform plugin
- [Shipwright documentation](https://shipwright.io/docs/)

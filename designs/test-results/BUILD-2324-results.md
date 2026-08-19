# BUILD-2324 — Live-Cluster Test Results: Volume Warning Contract & Migration Runbook

**Environment:** OpenShift **4.20.33** (server), `oc` client 4.17.16, Builds for Red Hat OpenShift Operator **v1.8.1** (operator-installed Shipwright), shipped `buildah` / `source-to-image` ClusterBuildStrategies.
**Scope:** End-to-end verification that converter-emitted volume warnings, `docs/volume-migration.md`, and the runbook match real Shipwright behavior. All fixtures were converter-shaped Builds (v1beta1) in throwaway namespace `b2324-thorough`.

## Result Matrix

| # | Scenario | Expected (per warning/docs) | Observed on live cluster | Verdict |
|---|----------|------------------------------|--------------------------|---------|
| B1 | Converter-shaped Build declaring `spec.volumes[my-npm-secret]` against shipped `buildah` (no such strategy volume) | `Registered=False`, reason `UndefinedVolume`, message quoted in warning/runbook | `Registered=False reason=UndefinedVolume` — message **exact match** with runbook wording: `Volume "my-npm-secret" is not defined in the Strategy` | ✅ PASS |
| B2 | BuildRun referencing an unregistered Build | Fails pre-pod (no pod scheduled), BuildRun surfaces registration failure | BuildRun failed before pod creation, condition points at Build registration error | ✅ PASS |
| B3 | Remediation per `docs/volume-migration.md`: copy shipped strategy → `buildah-with-volumes`, declare `my-npm-secret` (overridable) + mount, point Build at copy | Build re-registers | `Registered=True reason=Succeeded msg=all validations succeeded` (after spec touch — see B7) | ✅ PASS |
| B4 | Build overrides a strategy volume declared `overridable: false` | `Registered=False`, reason `VolumeNotOverridable` | `registered=False reason=VolumeNotOverridable message=Volume "locked-vol" is not overridable in the Strategy` | ✅ PASS |
| B5 | End-to-end BuildRun with secret volume; BuildRun-level override wins over Build-level value | Pod mounts Build's secret; a BuildRun `spec.volumes` override replaces the source; build completes and pushes | run2 pod: volume `my-npm-secret → secret npm-secret`, mounted in `step-build-and-push` at `/etc/npm` (readOnly). run3 pod (BuildRun override): `my-npm-secret → npm-secret-override`, same mount. run2 finished `Succeeded=True — All Steps have completed executing`; image pushed: `image-registry.openshift-image-registry.svc:5000/b2324-thorough/a2-npm@sha256:fd325767c114f0a464574cadaded46f693c9560820e0f018c1414936efebcf53` | ✅ PASS |
| B6 | `configMap` volume source (converter `a3-cm` fixture shape) against s2i copy `source-to-image-with-volumes` with overridable `build-settings` volume | Build registers | `registered=True reason=Succeeded msg=all validations succeeded` | ✅ PASS |
| B7 | Reconcile quirk: fixing the *strategy* alone does not re-register an already-failed Build (generation-based reconcile) | Runbook warns a Build spec touch is required | Build stayed `Registered=False` ≥30s after strategy fix; re-applying the Build with a spec change immediately flipped it to `Registered=True` | ✅ PASS |

## Notes

- **Warning contract verified verbatim:** the two failure messages quoted by the converter warning and runbook (`Volume "<name>" is not defined in the Strategy`, `Volume "<name>" is not overridable in the Strategy`) are byte-for-byte what Shipwright v1beta1 emits on this operator version.
- **Push access:** BuildRuns used the namespace `default` SA; `system:image-builder` was granted explicitly. Runbook already tells users to verify push credentials — no doc change needed.
- **B7 implication:** the runbook's "touch the Build spec after fixing the strategy" step is load-bearing; without it users will conclude the fix didn't work. Keep it prominent.
- **Cleanup:** namespace `b2324-thorough`, ClusterBuildStrategies `buildah-with-volumes`, `buildah-novr`, `source-to-image-with-volumes` all deleted after the run.

## Verdict

Migration guidance, converter warnings, and runbook messaging in this PR are **accurate against a live operator-installed Shipwright (v1.8.1 / OCP 4.20.33)**. No code or doc changes required from testing.

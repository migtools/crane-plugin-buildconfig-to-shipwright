# Bundled ClusterBuildStrategies

Copies of `clusterBuildStrategy/<name>/<name>.yaml` from
https://github.com/redhat-openshift-builds/strategy-catalog at the commit named
by `StrategyCatalogRef` in `../paramschema.go`. The converter reads only
`spec.parameters`; the files are kept whole so they diff cleanly against the
catalog.

Do not edit these by hand. Refresh them with
`hack/update-strategy-schemas.sh <commit>`. CI runs
`hack/update-strategy-schemas.sh --check` and fails when the bundle differs
from the pinned commit or from the catalog's `main`.

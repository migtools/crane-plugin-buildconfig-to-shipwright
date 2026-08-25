#!/usr/bin/env bash
# Keep buildconfig/strategies/ in sync with redhat-openshift-builds/strategy-catalog.
#
#   hack/update-strategy-schemas.sh <commit>   copy both strategy files at <commit>
#                                              and rewrite StrategyCatalogRef
#   hack/update-strategy-schemas.sh --check    diff the bundle against StrategyCatalogRef
#                                              and against the catalog's main
#
# --check exits 1 with a unified diff when either comparison differs and 2 when
# a catalog file could not be fetched. A diff against the pinned commit means
# the bundle was edited by hand. A diff against main means the catalog moved:
# re-run this script with the new commit and ship the bundle together with
# whatever converter change the new param needs.
#
# STRATEGY_CATALOG_RAW overrides the raw-content base URL (mirrors, tests; a
# file:// tree works).
set -euo pipefail

CATALOG_RAW="${STRATEGY_CATALOG_RAW:-https://raw.githubusercontent.com/redhat-openshift-builds/strategy-catalog}"
STRATEGIES=(buildah source-to-image)
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUNDLE="$ROOT/buildconfig/strategies"
SCHEMA_GO="$ROOT/buildconfig/paramschema.go"

usage() { # <exit code>
    cat <<'HELP'
usage: hack/update-strategy-schemas.sh <commit> | --check

  <commit>   copy both strategy files from strategy-catalog at <commit> (a hex
             SHA, 7 to 40 characters) into buildconfig/strategies/ and rewrite
             StrategyCatalogRef in buildconfig/paramschema.go
  --check    diff the bundle against StrategyCatalogRef (a difference means it
             was edited by hand) and against the catalog's main (a difference
             means the catalog moved); exit 1 on either, 2 when a fetch fails

STRATEGY_CATALOG_RAW overrides the raw-content base URL.
HELP
    exit "$1"
}

fetch() { # <ref> <strategy> <out-file>
    curl -sSfL --connect-timeout 10 --max-time 30 --retry 3 --retry-delay 2 --retry-connrefused \
        -o "$3" "$CATALOG_RAW/$1/clusterBuildStrategy/$2/$2.yaml"
}

current_ref() {
    sed -n 's/^const StrategyCatalogRef = "\([^"]*\)".*/\1/p' "$SCHEMA_GO"
}

# check_against <ref>: 0 when the bundle matches the catalog at <ref>, 1 when
# it differs (diff printed), 2 when a fetch failed and nothing was compared.
check_against() {
    local ref="$1" rc=0 s tmp
    tmp="$(mktemp -d)"
    for s in "${STRATEGIES[@]}"; do
        if ! fetch "$ref" "$s" "$tmp/$s.yaml"; then
            echo "could not fetch $s.yaml from strategy-catalog@$ref ($CATALOG_RAW); bundle not verified" >&2
            rm -rf "$tmp"
            return 2
        fi
        if ! diff -u -L "bundle/$s.yaml" -L "catalog@$ref/$s.yaml" "$BUNDLE/$s.yaml" "$tmp/$s.yaml"; then
            rc=1
        fi
    done
    rm -rf "$tmp"
    return $rc
}

case "${1:-}" in
--check)
    ref="$(current_ref)"
    [ -n "$ref" ] || { echo "cannot read StrategyCatalogRef from $SCHEMA_GO" >&2; exit 1; }
    worst=0
    echo "checking bundle against strategy-catalog@$ref (StrategyCatalogRef)"
    status=0; check_against "$ref" || status=$?
    [ "$status" -ne 1 ] || echo "bundle differs from the pinned commit; it was edited by hand" >&2
    [ "$status" -le "$worst" ] || worst=$status
    echo "checking bundle against strategy-catalog@main"
    status=0; check_against main || status=$?
    [ "$status" -ne 1 ] || echo "strategy-catalog main has moved; run: hack/update-strategy-schemas.sh <commit>" >&2
    [ "$status" -le "$worst" ] || worst=$status
    [ "$worst" -ne 0 ] || echo "bundle is current"
    exit "$worst"
    ;;
-h | --help)
    usage 0
    ;;
"" | -*)
    usage 2
    ;;
*)
    ref="$1"
    [[ "$ref" =~ ^[0-9a-f]{7,40}$ ]] || { echo "<commit> must be a hex SHA of 7 to 40 characters, got '$ref'" >&2; exit 2; }
    tmp="$(mktemp -d)"
    for s in "${STRATEGIES[@]}"; do
        if ! fetch "$ref" "$s" "$tmp/$s.yaml"; then
            echo "could not fetch $s.yaml from strategy-catalog@$ref ($CATALOG_RAW); bundle left unchanged" >&2
            rm -rf "$tmp"
            exit 2
        fi
    done
    for s in "${STRATEGIES[@]}"; do
        mv "$tmp/$s.yaml" "$BUNDLE/$s.yaml"
        echo "updated buildconfig/strategies/$s.yaml from strategy-catalog@$ref"
    done
    rmdir "$tmp"
    sed -i.bak "s|^const StrategyCatalogRef = \".*\"|const StrategyCatalogRef = \"$ref\"|" "$SCHEMA_GO"
    rm -f "$SCHEMA_GO.bak"
    [ "$(current_ref)" = "$ref" ] || { echo "failed to rewrite StrategyCatalogRef in $SCHEMA_GO" >&2; exit 1; }
    echo "StrategyCatalogRef = $ref"
    ;;
esac

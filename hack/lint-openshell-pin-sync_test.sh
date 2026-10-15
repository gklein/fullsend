#!/usr/bin/env bash
# lint-openshell-pin-sync_test.sh — tests for hack/lint-openshell-pin-sync
# against a fixture copy of the pin file and its hardcoded copies.
#
# Covers: the real copies pass; a pin bump that skips the copies fails and
# names every stale file; bumping everything passes; a reworded line, a
# duplicated line and a prefix-only match (0.1.23 vs 0.1.2) fail; a missing
# or malformed pin fails; an unregistered copy fails while a *_test.sh
# or *.test.* fixture is ignored; a submodule stays out of the sweep even
# with submodule.recurse=true; a non-git REPO_ROOT fails instead of passing.
#
# Run from the repo root:
#   bash hack/lint-openshell-pin-sync_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REAL_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
LINT="${SCRIPT_DIR}/lint-openshell-pin-sync"
FAILURES=0

PIN=".github/scripts/openshell-version.sh"
DOCS="docs/guides/user/running-agents-locally.md"
VM_FILES=(
  "hack/gitlab-runner-vm/create-gcp-vm.sh"
  "hack/gitlab-runner-vm/create-openshift-vm.sh"
  "hack/gitlab-runner-vm/setup.sh"
)

pass() { echo "ok       $*"; }
fail() {
  echo "FAIL     $*" >&2
  FAILURES=$((FAILURES + 1))
}

WORK=$(mktemp -d)
trap 'rm -rf "${WORK}"' EXIT
ROOT=""
OUT=""

# fresh_fixture copies the pin and every copy from the real tree into a new
# git repo, so each case starts from the current, in-sync state.
fresh_fixture() {
  ROOT=$(mktemp -d "${WORK}/root.XXXXXX")
  local f
  for f in "${PIN}" "${DOCS}" "${VM_FILES[@]}"; do
    mkdir -p "${ROOT}/$(dirname "${f}")"
    cp "${REAL_ROOT}/${f}" "${ROOT}/${f}"
  done
  git -C "${ROOT}" init -q
  git -C "${ROOT}" add -A
}

# replace FILE OLD NEW — literal-ish sed substitution, portable across BSD/GNU.
replace() {
  sed "s|$2|$3|g" "${ROOT}/$1" > "${ROOT}/$1.tmp"
  mv "${ROOT}/$1.tmp" "${ROOT}/$1"
}

run_lint() {
  git -C "${ROOT}" add -A 2>/dev/null || true
  OUT=$(REPO_ROOT="${ROOT}" "${LINT}" 2>&1) && return 0 || return 1
}

expect_pass() {
  if run_lint; then pass "$1"; else fail "$1 (expected pass): ${OUT}"; fi
}

# expect_fail NAME [SUBSTRING...] — lint fails and its output has each substring.
expect_fail() {
  local name="$1" s
  shift
  if run_lint; then
    fail "${name} (expected failure, got pass)"
    return
  fi
  for s in "$@"; do
    if [[ "${OUT}" != *"${s}"* ]]; then
      fail "${name} (output missing '${s}'): ${OUT}"
      return
    fi
  done
  pass "${name}"
}

CUR=$(sed -n 's/^OPENSHELL_VERSION=//p' "${REAL_ROOT}/${PIN}")
[[ "${CUR}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo "cannot read current pin from ${PIN}" >&2
  exit 1
}

# --- the real repository is in sync ----------------------------------------
if OUT=$("${LINT}" 2>&1); then pass "real tree in sync"; else fail "real tree: ${OUT}"; fi

# --- fixture starts in sync -------------------------------------------------
fresh_fixture
expect_pass "fixture in sync"

# --- manual pin bump without the copies ------------------------------------
fresh_fixture
replace "${PIN}" "^OPENSHELL_VERSION=.*" "OPENSHELL_VERSION=9.8.7"
expect_fail "pin bumped, copies stale" \
  "${DOCS}:" "pin in ${PIN} is 9.8.7" \
  "create-gcp-vm.sh" "create-openshift-vm.sh" "setup.sh"

# --- everything bumped together (what the Renovate group PR does) ----------
fresh_fixture
replace "${PIN}" "^OPENSHELL_VERSION=.*" "OPENSHELL_VERSION=9.8.7"
replace "${DOCS}" "OPENSHELL_VERSION=${CUR}" "OPENSHELL_VERSION=9.8.7"
replace "${DOCS}" "supervisor:${CUR}" "supervisor:9.8.7"
for f in "${VM_FILES[@]}"; do replace "${f}" "OPENSHELL_VERSION:-${CUR}" "OPENSHELL_VERSION:-9.8.7"; done
expect_pass "pin and copies bumped together"

# --- one copy stale ----------------------------------------------------------
fresh_fixture
replace "${DOCS}" "supervisor:${CUR}" "supervisor:0.0.1"
expect_fail "supervisor tag stale" "has OpenShell 0.0.1"

# --- reworded line drops out of the pattern ---------------------------------
fresh_fixture
replace "${DOCS}" "export OPENSHELL_VERSION=${CUR}" "OPENSHELL_VERSION=${CUR}; export OPENSHELL_VERSION"
expect_fail "reworded install line" "found 0"

# --- duplicated line is ambiguous --------------------------------------------
fresh_fixture
printf '\nexport OPENSHELL_VERSION=%s\n' "${CUR}" >> "${ROOT}/${DOCS}"
expect_fail "duplicated install line" "found 2"

# --- prefix-only match must not pass ----------------------------------------
fresh_fixture
replace "${VM_FILES[2]}" "OPENSHELL_VERSION:-${CUR}" "OPENSHELL_VERSION:-${CUR}3"
expect_fail "version with extra digit" "has OpenShell ${CUR}3"

# --- malformed or missing pin -----------------------------------------------
fresh_fixture
replace "${PIN}" "^OPENSHELL_VERSION=.*" "OPENSHELL_VERSION=latest"
expect_fail "non-semver pin" "expected exactly one"

fresh_fixture
rm "${ROOT}/${PIN}"
expect_fail "missing pin file" "pin file not found"

# --- unregistered copy is caught, test fixtures are not ---------------------
fresh_fixture
printf 'OPENSHELL_VERSION="${OPENSHELL_VERSION:-%s}"\n' "${CUR}" > "${ROOT}/new-script.sh"
expect_fail "unregistered copy" "UNTRACKED: new-script.sh:1:"

fresh_fixture
printf 'IMG="ghcr.io/nvidia/openshell/supervisor:0.0.1"\n' > "${ROOT}/some_test.sh"
printf 'const img = "ghcr.io/nvidia/openshell/supervisor:0.0.1";\n' > "${ROOT}/some.test.ts"
expect_pass "test fixtures ignored"

# --- submodules stay out of the sweep even with submodule.recurse=true -----
# (CI does not check out experiments/; a user-level git setting must not
# make the result differ from CI)
fresh_fixture
SUB_SRC=$(mktemp -d "${WORK}/sub.XXXXXX")
git -C "${SUB_SRC}" init -q
printf 'OPENSHELL_VERSION=0.0.43\n' > "${SUB_SRC}/old.sh"
git -C "${SUB_SRC}" add old.sh
git -C "${SUB_SRC}" -c user.name=t -c user.email=t@example.com commit -qm old
# A registered submodule: git grep never recurses into a bare embedded repo.
git -C "${ROOT}" -c protocol.file.allow=always submodule add -q "${SUB_SRC}" sub >/dev/null 2>&1
if [[ -z "$(git -C "${ROOT}" -c submodule.recurse=true grep -l '0\.0\.43' 2>/dev/null)" ]]; then
  fail "submodule fixture: git grep does not recurse into it, case proves nothing"
elif OUT=$(GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=submodule.recurse GIT_CONFIG_VALUE_0=true \
  REPO_ROOT="${ROOT}" "${LINT}" 2>&1); then
  pass "submodule ignored under submodule.recurse=true"
else
  fail "submodule ignored under submodule.recurse=true: ${OUT}"
fi

# --- outside a git repo the sweep fails closed ------------------------------
fresh_fixture
rm -rf "${ROOT}/.git"
if OUT=$(REPO_ROOT="${ROOT}" "${LINT}" 2>&1); then
  fail "non-git root (expected failure, got pass)"
elif [[ "${OUT}" == *"git grep failed"* ]]; then
  pass "non-git root fails closed"
else
  fail "non-git root (wrong error): ${OUT}"
fi

if [[ ${FAILURES} -gt 0 ]]; then
  echo "${FAILURES} test(s) failed" >&2
  exit 1
fi
echo "all lint-openshell-pin-sync tests passed"

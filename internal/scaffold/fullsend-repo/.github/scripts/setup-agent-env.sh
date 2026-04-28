#!/usr/bin/env bash
# Copy environment variables whose names start with AGENT_PREFIX into GITHUB_ENV,
# using the name with the prefix stripped (multiline-safe). AGENT_PREFIX must end
# with '_' (e.g. TRIAGE_). The workflow step should set AGENT_PREFIX and any
# AGENT_PREFIX* variables (e.g. secrets mapped under prefixed names).
#
# Note: The fullsend repository's own .github/scripts/setup-agent-env.sh uses
# STAGE_PREFIX instead of AGENT_PREFIX for its dogfooding workflows. This
# scaffold version uses AGENT_PREFIX to match the agent role naming convention.

set -euo pipefail

: "${GITHUB_ENV:?GITHUB_ENV must be set}"
: "${AGENT_PREFIX:?AGENT_PREFIX must be set}"

delim="ENV_${RANDOM}_${RANDOM}_$$"
while IFS= read -r name; do
  case "${name}" in
    "${AGENT_PREFIX}"*)
      dest="${name#"${AGENT_PREFIX}"}"
      [[ -n "${dest}" ]] || continue
      {
        printf '%s<<%s\n' "${dest}" "${delim}"
        printf '%s' "$(printenv "${name}")"
        printf '\n%s\n' "${delim}"
      } >> "${GITHUB_ENV}"
      ;;
  esac
done < <(compgen -e | sort -u)

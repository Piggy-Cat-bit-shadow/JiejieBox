#!/usr/bin/env bash
# Chooses the runner the Apple macOS jobs get: this account's own Mac while a runner of its pool is
# online, the hosted images when none is.
#
# Usage: apple-runner.sh        (writes runner=<json> and runner_macos_26=<json> to $GITHUB_OUTPUT)
#
# # Why the choice has to be made before the job is queued
#
# `runs-on: macos-latest` is a label list, not a preference. GitHub hands the job to the first idle
# runner carrying those labels; when none is, the job WAITS in the queue instead of falling back to
# another image. The Apple pipeline sat in exactly that queue for hours - "The job was not acquired
# by Runner of type hosted" - and a job cannot fix that from inside itself, because by the time it
# runs, the runner has already been chosen for it. The choice is therefore made by a job that needs
# no macOS runner at all, and is published as a job output the macOS jobs consume.
#
# # Fail-safe direction
#
# Every uncertainty - no token, an API refusal, an answer that is not a number - resolves to the
# HOSTED image. The one thing this script must never do is route a build to a machine that is
# asleep, because that is the queue it exists to escape and it would look identical to a hang.
#
# # What "online" means
#
# The repository runner list reports each runner's connection state, and GitHub flips it to offline
# when the runner's connection drops - which is what happens when the Mac sleeps. That state, not a
# local guess about uptime, is what decides. A runner that is online but BUSY is still "online": the
# job queues behind the one it is running, which is the honest behaviour for a single Mac and is
# visible in the run, unlike the hosted queue this replaces.
set -euo pipefail

pool="${APPLE_RUNNER_LABEL:-jiejie-apple}"
hosted='["macos-latest"]'
# ios-selfsign needs Swift tools 6.2 and has built on this image since it was written. The selector
# must not quietly move it to an image that cannot resolve the client's package graph, so the
# fallback is named per job rather than flattened to one label.
hosted_macos_26='["macos-26"]'

out="${GITHUB_OUTPUT:?GITHUB_OUTPUT is not set; this script runs as a workflow step}"

emit() {
  printf 'runner=%s\n' "$1" >> "$out"
  printf 'runner_macos_26=%s\n' "$2" >> "$out"
}

# A reason is a diagnostic first: the run log has to say WHY it went to the hosted image, or the
# fallback is indistinguishable from the selector never having been wired.
hosted_reason() {
  echo "::warning title=Apple runner::$1; the Apple jobs fall back to the hosted images" >&2
  emit "$hosted" "$hosted_macos_26"
  exit 0
}

if [ -z "${GH_TOKEN:-}" ]; then
  hosted_reason "APPLE_RUNNER_TOKEN is not set"
fi
if [ -z "${GITHUB_REPOSITORY:-}" ]; then
  hosted_reason "GITHUB_REPOSITORY is not set"
fi
if ! command -v jq >/dev/null; then
  hosted_reason "jq is missing"
fi

# `gh api` exits non-zero on a 401/403/5xx; that is a refusal, not an empty fleet.
if ! listing="$(gh api "repos/${GITHUB_REPOSITORY}/actions/runners" 2>/dev/null)"; then
  hosted_reason "the repository's runner list could not be read"
fi

if ! online="$(printf '%s' "$listing" | jq --arg pool "$pool" \
  '[.runners[] | select(.status == "online") | select([.labels[].name] | index($pool))] | length')"; then
  hosted_reason "the runner list could not be interpreted"
fi

case "$online" in
  '' | *[!0-9]*) hosted_reason "the runner count was not a number: '$online'" ;;
esac

if [ "$online" -gt 0 ]; then
  echo "the self-hosted Apple runner is online; the macOS jobs run on this machine"
  emit "[\"self-hosted\",\"${pool}\"]" "[\"self-hosted\",\"${pool}\"]"
else
  echo "no self-hosted Apple runner is online; the macOS jobs use the hosted images"
  emit "$hosted" "$hosted_macos_26"
fi

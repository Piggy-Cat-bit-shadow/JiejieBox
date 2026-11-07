#!/usr/bin/env bash
set -euo pipefail

tag="v${VERSION:?}"
gate=./scripts/ci/release-artifacts.sh
repo="${GITHUB_REPOSITORY:?}"
test "$(git rev-parse HEAD)" = "${GITHUB_SHA:?}"
test "$(cat release/JIEJIE_VERSION)" = "$VERSION"
printf '%s  upload/SHA256SUMS\n' "${BUNDLE_HASHES:?}" | sha256sum -c -
(cd upload && sha256sum -c SHA256SUMS)
"$gate" check-upload --out upload --tag "$tag" --version "$VERSION"
assets="$("$gate" publish-args --dir upload --tag "$tag" --version "$VERSION")"

# Tag only after the staged bundle has passed all checks. A rerun can reuse
# the same tag, and a concurrent change cannot move an existing tag.
if git ls-remote --exit-code --refs origin "refs/tags/$tag" > remote-tag; then
  echo "Checking existing tag $tag"
else
  test "$?" -eq 2
  git -c user.name='JiejieBox release' -c user.email='actions@users.noreply.github.com' \
    tag -a "$tag" -m "Release $tag" "$GITHUB_SHA"
  git push origin "refs/tags/$tag"
fi
git fetch origin "refs/tags/$tag:refs/tags/$tag"
test "$(git rev-parse "$tag^{commit}")" = "$GITHUB_SHA"

# Find a draft as well as a public release. Only add absent assets to a draft.
# Conflicting assets are never overwritten.
gh api --paginate --slurp "repos/$repo/releases?per_page=100" |
  jq -e --arg tag "$tag" '[.[][] | select(.tag_name == $tag)] | if length <= 1 then .[0] // {} else error("duplicate release") end' > release.json
if [ "$(jq -r '.id // empty' release.json)" = "" ]; then
  gh release create "$tag" --verify-tag --draft --title "$tag" --notes-file .github/RELEASE_NOTES.md
  draft=true
else
  draft="$(jq -r '.draft' release.json)"
  test "$(jq -r '.prerelease' release.json)" = false
fi
verify_assets() {
  local allow_missing="$1" path name digest
  local -a missing=()
  gh release view "$tag" --json databaseId --jq '.databaseId' > release-id
  gh api --paginate --slurp "repos/$repo/releases/$(cat release-id)/assets?per_page=100" |
    jq '[.[][]]' > release-assets.json
  while IFS= read -r name; do
    grep -Fxq "upload/$name" <<<"$assets"
  done < <(jq -r '.[].name' release-assets.json)
  while IFS= read -r path; do
    name="${path##*/}"
    digest="$(jq -r --arg name "$name" '.[] | select(.name == $name) | .digest // "missing-digest"' release-assets.json)"
    if [ -z "$digest" ] && [ "$allow_missing" = true ]; then
      missing+=("$path")
    else
      test "$digest" = "sha256:$(sha256sum "$path" | cut -d ' ' -f 1)"
    fi
  done <<<"$assets"
  if [ "${#missing[@]}" -gt 0 ]; then
    gh release upload "$tag" "${missing[@]}"
  fi
}
verify_assets "$draft"
verify_assets false
if [ "$draft" = true ]; then
  gh release edit "$tag" --draft=false --prerelease=false
fi
verify_assets false
gh release view "$tag" --json isDraft,isPrerelease,url --jq 'select(.isDraft == false and .isPrerelease == false) | .url' > release-url
test -s release-url
echo "Published $tag at $GITHUB_SHA: $(cat release-url)" >> "$GITHUB_STEP_SUMMARY"

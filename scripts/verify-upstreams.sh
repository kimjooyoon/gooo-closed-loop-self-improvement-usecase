#!/usr/bin/env bash
set -euo pipefail

repo_root="${GITHUB_WORKSPACE:?GITHUB_WORKSPACE is required}"
out_dir="${1:?caller-owned output directory is required}"
lock_file="$repo_root/contracts/upstream-release-locks.json"
mkdir -p "$out_dir/assets"

jq -e '.optional == true and (.releases | length == 5) and .composition_boundary.status == "UNKNOWN"' "$lock_file" >/dev/null
verified='[]'

while IFS= read -r row; do
  key=$(jq -r '.key' <<<"$row")
  repository=$(jq -r '.repository' <<<"$row")
  tag=$(jq -r '.tag' <<<"$row")
  expected_release=$(jq -r '.release_id' <<<"$row")
  expected_tag_object=$(jq -r '.tag_object_sha' <<<"$row")
  expected_target=$(jq -r '.target_sha' <<<"$row")
  expected_asset_id=$(jq -r '.asset.id' <<<"$row")
  expected_asset_name=$(jq -r '.asset.name' <<<"$row")
  expected_asset_size=$(jq -r '.asset.size_bytes' <<<"$row")
  expected_asset_digest=$(jq -r '.asset.digest' <<<"$row")

  release=$(gh api "repos/$repository/releases/tags/$tag")
  test "$(jq -r '.id' <<<"$release")" = "$expected_release"
  test "$(jq -r '.tag_name' <<<"$release")" = "$tag"
  test "$(jq -r '.immutable' <<<"$release")" = "true"

  ref=$(gh api "repos/$repository/git/ref/tags/$tag")
  test "$(jq -r '.object.type' <<<"$ref")" = "tag"
  tag_object=$(jq -r '.object.sha' <<<"$ref")
  test "$tag_object" = "$expected_tag_object"
  tag_record=$(gh api "repos/$repository/git/tags/$tag_object")
  test "$(jq -r '.object.sha' <<<"$tag_record")" = "$expected_target"

  asset=$(jq -c --argjson id "$expected_asset_id" '.assets[] | select(.id == $id)' <<<"$release")
  test "$(jq -r '.name' <<<"$asset")" = "$expected_asset_name"
  test "$(jq -r '.size' <<<"$asset")" = "$expected_asset_size"
  test "$(jq -r '.digest' <<<"$asset")" = "$expected_asset_digest"

  asset_path="$out_dir/assets/$key"
  curl --fail --location --retry 3 --silent --show-error "https://github.com/$repository/releases/download/$tag/$expected_asset_name" -o "$asset_path"
  test "$(stat -c '%s' "$asset_path")" = "$expected_asset_size"
  test "sha256:$(sha256sum "$asset_path" | awk '{print $1}')" = "$expected_asset_digest"

  verified=$(jq --arg key "$key" --arg repository "$repository" --arg tag "$tag" --arg tag_object "$tag_object" --arg target "$(jq -r '.object.sha' <<<"$tag_record")" --arg asset "$expected_asset_name" --arg digest "$expected_asset_digest" --argjson release "$expected_release" --argjson asset_id "$expected_asset_id" --argjson size "$expected_asset_size" '. + [{key:$key,repository:$repository,tag:$tag,release_id:$release,immutable:true,tag_object_sha:$tag_object,target_sha:$target,asset:{id:$asset_id,name:$asset,size_bytes:$size,digest:$digest},downloaded_and_verified:true}]' <<<"$verified")
done < <(jq -c '.releases[]' "$lock_file")

jq -n --argjson releases "$verified" --arg lock_sha256 "$(sha256sum "$lock_file" | awk '{print $1}')" --argjson boundary "$(jq -c '.composition_boundary' "$lock_file")" '{schema:"gooo/upstream-verification/v1",lock_sha256:$lock_sha256,verified_count:($releases|length),releases:$releases,composition_boundary:$boundary}' > "$out_dir/upstream-verification.json"

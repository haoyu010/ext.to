#!/usr/bin/env bash
# Bump the release version, then commit and tag it.
#
# The version lives in VERSION and is read by the Dockerfile and the workflow,
# so the image tag, the panel footer and the compiled-in value always agree.
# Run this before pushing a change that should become a new release:
#
#   ./scripts/bump-version.sh patch    # 1.0.0 -> 1.0.1
#   ./scripts/bump-version.sh minor    # 1.0.1 -> 1.1.0
#   ./scripts/bump-version.sh major    # 1.1.0 -> 2.0.0
set -euo pipefail

cd "$(dirname "$0")/.."

part="${1:-patch}"
case "$part" in
  major|minor|patch) ;;
  *) echo "usage: $0 [major|minor|patch]" >&2; exit 2 ;;
esac

current=$(tr -d ' \t\r\n' < VERSION)
if ! printf '%s' "$current" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "VERSION must be MAJOR.MINOR.PATCH, got: '$current'" >&2
  exit 1
fi

IFS=. read -r major minor patch <<<"$current"
case "$part" in
  major) major=$((major + 1)); minor=0; patch=0 ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  patch) patch=$((patch + 1)) ;;
esac

next="$major.$minor.$patch"
printf '%s' "$next" > VERSION
echo "$current -> $next"

# Only tag when the tree is clean, so the tag cannot point at a commit that
# still needs edits.
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "commit the VERSION bump, then tag:" >&2
  echo "  git commit -am 'Release $next' && git tag -a v$next -m 'Release $next'" >&2
  echo "  git push origin main --tags" >&2
  exit 0
fi

git commit -am "Release $next"
git tag -a "v$next" -m "Release $next"
echo "tagged v$next; push with: git push origin main --tags"

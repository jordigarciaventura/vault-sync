#!/bin/sh
# Bump the version in VERSION, commit it and create the matching annotated tag.
#
#   scripts/bump-version.sh [patch|fix|minor|major]   (default: patch)
#
# Run through `task bump`. Nothing is pushed: push the commit and the tag
# yourself to trigger the Release workflow (see DEVELOPMENT.md → Releasing).
set -eu

part=${1:-patch}
root=$(git rev-parse --show-toplevel)
file="$root/VERSION"

die() {
	echo "bump: $*" >&2
	exit 1
}

case "$part" in
	patch | fix) part=patch ;;
	minor | major) ;;
	*) die "unknown part '$part'; use patch (default), minor or major" ;;
esac

[ -f "$file" ] || die "$file not found"
current=$(tr -d ' \t\r\n' <"$file")
printf '%s' "$current" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' ||
	die "VERSION must contain MAJOR.MINOR.PATCH, got '$current'"

branch=$(git rev-parse --abbrev-ref HEAD)
[ "$branch" = main ] || die "releases are cut from main (current: $branch); run 'git switch main' first"
[ -z "$(git status --porcelain)" ] || die "working tree is not clean; commit or stash your changes first"

IFS=. read -r major minor patch <<VERSION_EOF
$current
VERSION_EOF

case "$part" in
	major) major=$((major + 1)) minor=0 patch=0 ;;
	minor) minor=$((minor + 1)) patch=0 ;;
	patch) patch=$((patch + 1)) ;;
esac
next="$major.$minor.$patch"
tag="v$next"

git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "tag $tag already exists"

printf '%s\n' "$next" >"$file"
git add "$file"
git commit -q -m "chore(release): $tag"
git tag -a "$tag" -m "$tag"

echo "Bumped $current -> $next (commit + tag $tag)."
echo "Publish with: git push origin main $tag"

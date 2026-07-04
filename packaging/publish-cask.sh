#!/usr/bin/env bash
# Render the Homebrew cask from packaging/k8s-tray-forwarder.rb.tmpl and push it
# to the tap. Replaces GoReleaser's cask generation, which (in the OSS edition)
# can only emit a `binary` stanza — we need an `app` stanza so `brew install`
# drops a launchable .app into /Applications instead of a CLI binary.
#
# Usage:
#   packaging/publish-cask.sh --version VERSION --zip PATH [--dry-run]
#
# Env (required unless --dry-run):
#   HOMEBREW_TAP_GITHUB_TOKEN  PAT with push access to the tap repo.
# Env (optional, with defaults):
#   TAP_OWNER   (laszukdawid)
#   TAP_REPO    (homebrew-tap)
#   TAP_BRANCH  (main)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="$SCRIPT_DIR/k8s-tray-forwarder.rb.tmpl"

VERSION=""
ZIP=""
DRY_RUN=false

while [[ $# -gt 0 ]]; do
	case "$1" in
	--version) VERSION="$2"; shift 2 ;;
	--zip) ZIP="$2"; shift 2 ;;
	--dry-run) DRY_RUN=true; shift ;;
	*) echo "publish-cask.sh: unknown argument: $1" >&2; exit 2 ;;
	esac
done

if [[ -z "$VERSION" || -z "$ZIP" ]]; then
	echo "publish-cask.sh: --version and --zip are required" >&2
	exit 2
fi
VERSION="${VERSION#v}" # v0.1.0 -> 0.1.0
if [[ ! -f "$ZIP" ]]; then
	echo "publish-cask.sh: zip not found: $ZIP" >&2
	exit 1
fi

SHA256="$(shasum -a 256 "$ZIP" | awk '{print $1}')"

# Render the cask. Use a literal-safe delimiter for the sha (hex, so '/' is safe)
# and the version.
CASK="$(sed -e "s/__VERSION__/${VERSION}/g" -e "s/__SHA256__/${SHA256}/g" "$TEMPLATE")"

if $DRY_RUN; then
	echo "--- rendered cask (version $VERSION, sha256 $SHA256) ---"
	echo "$CASK"
	exit 0
fi

: "${HOMEBREW_TAP_GITHUB_TOKEN:?HOMEBREW_TAP_GITHUB_TOKEN must be set}"
TAP_OWNER="${TAP_OWNER:-laszukdawid}"
TAP_REPO="${TAP_REPO:-homebrew-tap}"
TAP_BRANCH="${TAP_BRANCH:-main}"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "publish-cask.sh: cloning $TAP_OWNER/$TAP_REPO"
git clone --depth 1 --branch "$TAP_BRANCH" \
	"https://x-access-token:${HOMEBREW_TAP_GITHUB_TOKEN}@github.com/${TAP_OWNER}/${TAP_REPO}.git" \
	"$WORK/tap"

mkdir -p "$WORK/tap/Casks"
CASK_PATH="$WORK/tap/Casks/k8s-tray-forwarder.rb"
printf '%s\n' "$CASK" >"$CASK_PATH"

cd "$WORK/tap"
if git diff --quiet -- Casks/k8s-tray-forwarder.rb; then
	echo "publish-cask.sh: cask already up to date, nothing to push"
	exit 0
fi

git config user.name "goreleaserbot"
git config user.email "bot@goreleaser.com"
git add Casks/k8s-tray-forwarder.rb
git commit -m "Brew cask update for k8s-tray-forwarder version v${VERSION}"
git push origin "$TAP_BRANCH"
echo "publish-cask.sh: pushed cask v${VERSION} to $TAP_OWNER/$TAP_REPO"

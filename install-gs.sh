#!/usr/bin/env sh
set -eu

REPO_URL="${GITSLICE_REPO_URL:-https://github.com/niczy/gitslice.git}"
REF="${GITSLICE_REF:-main}"
GOPATH_VALUE="$(go env GOPATH)"
INSTALL_DIR="${GITSLICE_INSTALL_DIR:-${GOBIN:-${GOPATH_VALUE}/bin}}"

for tool in go git make protoc; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "error: $tool is required to install gs" >&2
		exit 1
	fi
done

workdir="$(mktemp -d "${TMPDIR:-/tmp}/gitslice-install.XXXXXX")"
cleanup() {
	rm -rf "$workdir"
}
trap cleanup EXIT INT TERM

git clone --depth 1 --branch "$REF" "$REPO_URL" "$workdir/repo"

cd "$workdir/repo"
make install
make build-cli

mkdir -p "$INSTALL_DIR"
tmp="$INSTALL_DIR/.gs.new.$$"
cp bin/gs "$tmp"
chmod +x "$tmp"
mv "$tmp" "$INSTALL_DIR/gs"

echo "installed gs to $INSTALL_DIR/gs"

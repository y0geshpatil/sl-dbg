#!/usr/bin/env bash
# Check a local GoReleaser snapshot before a maintainer publishes a tag.
set -euo pipefail
DIST="${1:?usage: verify-release.sh DIST JAVA_TARGET}"
JAVA_TARGET="${2:?usage: verify-release.sh DIST JAVA_TARGET}"
if command -v sha256sum >/dev/null 2>&1; then
  verify() { sha256sum -c "$1"; }
else
  verify() { shasum -a 256 -c "$1"; }
fi
(
  cd "$DIST"
  manifests=(sl-dbg_*_checksums.txt)
  [ "${#manifests[@]}" -eq 1 ]
  [ -s "${manifests[0]}" ]
  verify "${manifests[0]}"
  for os in darwin linux; do
    for arch in amd64 arm64; do
      archives=(sl-dbg_*_"${os}_${arch}".tar.gz)
      [ "${#archives[@]}" -eq 1 ]
      [ -s "${archives[0]}" ]
      contents="$(tar -tzf "${archives[0]}")"
      for entry in sl-dbg LICENSE README.md docs/COMMANDS.md; do
        printf '%s\n' "$contents" | grep -Fx "$entry" >/dev/null
      done
    done
  done
)
(
  cd "$JAVA_TARGET"
  test -s sl-dbg-java-adapter.jar
  verify sl-dbg-java-adapter.jar.sha256
  jar tf sl-dbg-java-adapter.jar | grep -Fx 'com/sldbg/java/Launcher.class' >/dev/null
)
echo "Release archives and Java adapter checksums verified."

#!/usr/bin/env bash
# Generate, compile and run production-shaped clients and servers from real proto.
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
case "$(uname -s)-$(uname -m)" in
  Darwin-*)
    archive=protoc-33.0-osx-universal_binary.zip
    checksum=88c0a52f048827d6892cd3403e3ae4181208ab261f93428c86d1736f536a60ec ;;
  Linux-x86_64)
    archive=protoc-33.0-linux-x86_64.zip
    checksum=d99c011b799e9e412064244f0be417e5d76c9b6ace13a2ac735330fa7d57ad8f ;;
  *) echo "Unsupported protoc platform: $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac
cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/spine/generator-tests"
mkdir -p "$cache_dir"
if [[ -f "$cache_dir/$archive" ]]; then
  cp "$cache_dir/$archive" "$work_dir/protoc.zip"
else
  curl -fsSL --retry 3 --retry-all-errors --connect-timeout 15 --max-time 120 \
    "https://github.com/protocolbuffers/protobuf/releases/download/v33.0/$archive" -o "$work_dir/protoc.zip"
fi
if command -v sha256sum >/dev/null; then
  actual=$(sha256sum "$work_dir/protoc.zip" | cut -d' ' -f1)
else
  actual=$(shasum -a 256 "$work_dir/protoc.zip" | cut -d' ' -f1)
fi
[[ "$actual" == "$checksum" ]] || { echo 'protoc checksum mismatch' >&2; exit 1; }
cp "$work_dir/protoc.zip" "$cache_dir/$archive.tmp.$$"
mv "$cache_dir/$archive.tmp.$$" "$cache_dir/$archive"
unzip -q "$work_dir/protoc.zip" -d "$work_dir/protoc"
mkdir -p "$work_dir/bin" "$work_dir/consumer"
GOBIN="$work_dir/bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
GOBIN="$work_dir/bin" go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
go -C "$repo_root/cmd/protoc-gen-go-http" build -o "$work_dir/bin/protoc-gen-go-http" .
go -C "$repo_root/cmd/protoc-gen-go-errors" build -o "$work_dir/bin/protoc-gen-go-errors" .
export PATH="$work_dir/bin:$PATH"
"$work_dir/protoc/bin/protoc" --version
protoc-gen-go-errors --version
protoc-gen-go-http --version
"$work_dir/protoc/bin/protoc" \
  -I "$repo_root/internal/testdata/generators" -I "$repo_root/third_party" \
  -I "$repo_root" -I "$work_dir/protoc/include" \
  --go_out=paths=source_relative:"$work_dir/consumer" \
  --go-grpc_out=paths=source_relative:"$work_dir/consumer" \
  --go-http_out=paths=source_relative:"$work_dir/consumer" \
  --go-errors_out=paths=source_relative:"$work_dir/consumer" fixture.proto nested.proto plain.proto codes/reasons.proto
[[ ! -f "$work_dir/consumer/plain_http.pb.go" ]] || { echo 'unannotated service generated HTTP bindings' >&2; exit 1; }
[[ ! -f "$work_dir/consumer/plain_errors.pb.go" ]] || { echo 'unannotated enum generated error helpers' >&2; exit 1; }
for scenario in invalid_code invalid_low_code invalid_names invalid_http invalid_stream_body; do
  plugin=go-errors
  if [[ "$scenario" == invalid_http || "$scenario" == invalid_stream_body ]]; then plugin=go-http; fi
  if "$work_dir/protoc/bin/protoc" \
    -I "$repo_root/internal/testdata/generators" -I "$repo_root/third_party" \
    -I "$repo_root" -I "$work_dir/protoc/include" \
    "--${plugin}_out=paths=source_relative:$work_dir/consumer" "$scenario.proto" > "$work_dir/$scenario.log" 2>&1; then
    echo "invalid input unexpectedly accepted: $scenario" >&2; exit 1
  fi
  if grep -q 'panic:' "$work_dir/$scenario.log"; then cat "$work_dir/$scenario.log"; exit 1; fi
  case "$scenario" in
    invalid_code|invalid_low_code) grep -q 'error code must be 0 or between' "$work_dir/$scenario.log" ;;
    invalid_names) grep -q 'duplicate error helper' "$work_dir/$scenario.log" ;;
    invalid_http) grep -q 'corresponding field' "$work_dir/$scenario.log" ;;
    invalid_stream_body) grep -q 'singular messages' "$work_dir/$scenario.log" ;;
  esac
done
cp "$repo_root/internal/testdata/generators/fixture_test.go" "$work_dir/consumer/"
cat > "$work_dir/consumer/go.mod" <<'MOD'
module example.com/spine-generator-test

go 1.27.3

require github.com/apus-run/spine/v3 v3.0.0
MOD
go -C "$work_dir/consumer" mod edit "-replace=github.com/apus-run/spine/v3=$repo_root"
go -C "$work_dir/consumer" mod edit "-replace=github.com/apus-run/spine/cmd/protoc-gen-go-errors/v3=$repo_root/cmd/protoc-gen-go-errors"
go -C "$work_dir/consumer" mod tidy
modules=$(go -C "$work_dir/consumer" list -m all)
if grep -q 'github.com/apus-run/spine/cmd/protoc-gen-go-errors' <<< "$modules"; then
  echo 'generated application must not depend on the errors generator module' >&2; exit 1
fi
go -C "$work_dir/consumer" test -v -race -count=1 -timeout=90s ./...
go -C "$work_dir/consumer" vet ./...

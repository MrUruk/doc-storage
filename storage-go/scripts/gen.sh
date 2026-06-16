#!/usr/bin/env bash
# Regenerate Go protobuf + Connect stubs from proto/.
# Requires: protoc, and the Go plugins on PATH:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
set -euo pipefail
cd "$(dirname "$0")/.."

protoc -I proto \
  --go_out=. --go_opt=module=github.com/mruruk/doc-storage/storage-go \
  --connect-go_out=. --connect-go_opt=module=github.com/mruruk/doc-storage/storage-go \
  proto/storage/v1/storage.proto proto/pandoc/v1/pandoc.proto

echo "Generated gen/storage/v1 and gen/pandoc/v1"

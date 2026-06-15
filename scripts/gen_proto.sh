#!/usr/bin/env bash
# Regenerate the Connect-RPC Python stubs from proto/.
# Requires the dev extras installed: pip install -e ".[dev]"
set -euo pipefail

cd "$(dirname "$0")/.."

PLUGIN="$(python -c 'import shutil,sys; print(shutil.which("protoc-gen-connecpy") or "")')"
if [[ -z "${PLUGIN}" ]]; then
  echo "protoc-gen-connecpy not found. Run: pip install -e '.[dev]'" >&2
  exit 1
fi
# The plugin ships as a binary without the exec bit on some installs.
chmod +x "${PLUGIN}" 2>/dev/null || true

python -m grpc_tools.protoc \
  -Iproto \
  --python_out=src \
  --pyi_out=src \
  --plugin=protoc-gen-connecpy="${PLUGIN}" \
  --connecpy_out=src \
  proto/storage/v1/storage.proto

echo "Generated src/storage/v1/storage_pb2.py and storage_connecpy.py"

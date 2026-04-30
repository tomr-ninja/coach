#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COACH_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

IMAGE_NAME="coach-example-basic:latest"

echo "=== Building model image: $IMAGE_NAME ==="
docker build -t "$IMAGE_NAME" "$SCRIPT_DIR"

echo ""
echo "=== Running coach ==="
(cd "$COACH_DIR" && go run ./cmd/coach run \
    -model "$IMAGE_NAME" \
    -data "$SCRIPT_DIR/data" \
    -output "$SCRIPT_DIR/output")

echo ""
echo "=== Artifact ==="
for d in "$SCRIPT_DIR"/output/*/; do
    echo "Fingerprint: $(basename "$d")"
    echo "Contents:"
    cat "$d"/sorted_chunks.txt 2>/dev/null || echo "  (no sorted_chunks.txt)"
done

#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COACH_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

IMAGE_NAME="coach-example-mnist-pytorch:latest"
DATA_DIR="$SCRIPT_DIR/data"
OUTPUT_DIR="$SCRIPT_DIR/output"

# Ensure data is available
if [ ! -d "$DATA_DIR/mnist" ]; then
    echo "MNIST data not found. Running get-data.py ..."
    python3 "$SCRIPT_DIR/get-data.py"
fi

echo "=== Building model image: $IMAGE_NAME ==="
docker build -t "$IMAGE_NAME" "$SCRIPT_DIR"

echo ""
echo "=== Running coach ==="
echo "  model:  $IMAGE_NAME"
echo "  data:   $DATA_DIR"
echo "  output: $OUTPUT_DIR"
(cd "$COACH_DIR" && go run ./cmd/coach run \
    -model "$IMAGE_NAME" \
    -data "$DATA_DIR" \
    -output "$OUTPUT_DIR")

echo ""
echo "=== Artifact ==="
for d in "$OUTPUT_DIR"/*/; do
    echo "Fingerprint: $(basename "$d")"
    echo "  model.pt exists:     $([ -f "$d/model.pt" ] && echo yes || echo no)"
    echo "  metrics.json exists: $([ -f "$d/metrics.json" ] && echo yes || echo no)"
    if [ -f "$d/metrics.json" ]; then
        echo "  --- metrics.json ---"
        cat "$d/metrics.json"
    fi
done

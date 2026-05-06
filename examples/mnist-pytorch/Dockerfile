# Stage 1: resolve dependencies with uv, export to requirements.txt
FROM python:3.14-slim AS builder

WORKDIR /workspace

COPY pyproject.toml uv.lock ./
RUN pip install --no-cache-dir uv \
    && uv export --frozen --no-dev --no-hashes --extra-index-url https://download.pytorch.org/whl/cpu -o requirements.txt

# Stage 2: minimal runtime image — no uv, just pip + requirements.txt
FROM python:3.14-slim

WORKDIR /workspace

COPY --from=builder /workspace/requirements.txt ./
RUN pip install --no-cache-dir --extra-index-url https://download.pytorch.org/whl/cpu -r requirements.txt \
    && rm requirements.txt \
    && find /usr/local -type d -name "__pycache__" -exec rm -rf {} + 2>/dev/null || true \
    && rm -rf /root/.cache /tmp/*

COPY scripts/ /scripts/
COPY train.py /workspace/train.py

ENTRYPOINT ["python", "/workspace/train.py"]

FROM python:3.14-slim

WORKDIR /workspace

# Install uv and resolve dependencies from the lock file
COPY pyproject.toml uv.lock ./
RUN pip install --no-cache-dir uv \
    && uv export --frozen --no-dev -o requirements.txt \
    && uv pip sync --system --no-cache-dir requirements.txt \
    && rm requirements.txt

COPY scripts/ /scripts/
COPY train.py /workspace/train.py

ENTRYPOINT ["python", "/workspace/train.py"]

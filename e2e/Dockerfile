# Unified E2E runner — the data-driven per-tool matrix.
# Select via TEST_ID (only 00 today); defaults to 00.
FROM docker.io/library/python:3.13-slim

WORKDIR /app
ENV PYTHONPATH=/app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir grpcio protobuf pyyaml nats-py==2.9.0 redis==5.2.1

# Shared harness + generated proto stubs.
COPY e2e/tests/workspace_common /app/workspace_common
COPY e2e/gen                    /app/gen

# The tool catalog drives the per-tool case matrix.
COPY internal/adapters/tools/catalog_defaults.yaml /app/catalog_defaults.yaml

# The per-tool matrix runner, its spec loader, and the adversarial spec matrix
# (spec_runner resolves SPECS_DIR relative to its own location: /app/specs).
COPY e2e/tests/00-tool-matrix/test_tool_matrix.py /app/tests/00/test.py
COPY e2e/tests/00-tool-matrix/spec_runner.py      /app/spec_runner.py
COPY e2e/tests/00-tool-matrix/specs               /app/specs

RUN groupadd -r testuser && useradd -r -m -g testuser -u 1000 testuser && \
    chown -R testuser:testuser /app

USER testuser

# Entrypoint: python /app/tests/${TEST_ID}/test.py
CMD ["sh", "-c", "exec python /app/tests/${TEST_ID:-00}/test.py"]

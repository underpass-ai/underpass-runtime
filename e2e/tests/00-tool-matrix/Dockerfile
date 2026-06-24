FROM docker.io/library/python:3.13-slim

WORKDIR /app
ENV PYTHONPATH=/app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY e2e/tests/workspace_common /app/workspace_common
COPY e2e/tests/00-tool-matrix/test_tool_matrix.py /app/test_tool_matrix.py
# The tool catalog is the source of truth that drives the per-tool case matrix.
COPY internal/adapters/tools/catalog_defaults.yaml /app/catalog_defaults.yaml

RUN pip install --no-cache-dir grpcio protobuf pyyaml

COPY e2e/gen /app/gen
RUN groupadd -r testuser && useradd -r -m -g testuser -u 1000 testuser && \
    chown -R testuser:testuser /app

USER testuser

CMD ["python", "/app/test_tool_matrix.py"]

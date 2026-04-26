FROM docker.io/library/python:3.13-slim

WORKDIR /app
ENV PYTHONPATH=/app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY e2e/tests/workspace_common /app/workspace_common
COPY e2e/tests/24-runtime-saturation-notify-tools/test_runtime_saturation_notify_tools.py /app/test_runtime_saturation_notify_tools.py

RUN pip install --no-cache-dir grpcio protobuf

COPY e2e/gen /app/gen
RUN groupadd -r testuser && useradd -r -m -g testuser -u 1000 testuser && \
    chown -R testuser:testuser /app

USER testuser

CMD ["python", "/app/test_runtime_saturation_notify_tools.py"]

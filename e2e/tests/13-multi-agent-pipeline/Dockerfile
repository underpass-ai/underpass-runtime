FROM docker.io/library/python:3.13-slim

WORKDIR /app
ENV PYTHONPATH=/app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir nats-py==2.9.0

COPY e2e/tests/workspace_common /app/workspace_common
COPY e2e/tests/13-multi-agent-pipeline/test_multi_agent_pipeline.py /app/test_multi_agent_pipeline.py

RUN groupadd -r testuser && useradd -r -m -g testuser -u 1000 testuser && \
    chown -R testuser:testuser /app

USER testuser

CMD ["python", "/app/test_multi_agent_pipeline.py"]

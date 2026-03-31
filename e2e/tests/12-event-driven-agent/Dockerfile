FROM docker.io/library/python:3.13-slim

WORKDIR /app
ENV PYTHONPATH=/app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir nats-py==2.9.0

COPY e2e/tests/workspace_common /app/workspace_common
COPY e2e/tests/12-event-driven-agent/test_event_driven_agent.py /app/test_event_driven_agent.py

RUN pip install --no-cache-dir grpcio protobuf

COPY e2e/gen /app/gen
RUN groupadd -r testuser && useradd -r -m -g testuser -u 1000 testuser && \
    chown -R testuser:testuser /app

USER testuser

CMD ["python", "/app/test_event_driven_agent.py"]

FROM docker.io/library/python:3.13-slim

WORKDIR /app
ENV PYTHONPATH=/app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir pyyaml \
    && groupadd -r testuser && useradd -r -m -g testuser -u 1000 testuser

COPY e2e/tests/workspace_common /app/workspace_common
COPY e2e/tests/15-workspace-vllm-tool-orchestration/tool_catalog.yaml /app/tool_catalog.yaml
COPY e2e/tests/15-workspace-vllm-tool-orchestration/test_workspace_vllm_tool_orchestration.py /app/test_workspace_vllm_tool_orchestration.py
USER testuser

CMD ["python", "/app/test_workspace_vllm_tool_orchestration.py"]

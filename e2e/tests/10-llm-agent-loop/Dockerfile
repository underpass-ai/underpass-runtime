FROM docker.io/library/python:3.13-slim

RUN pip install --no-cache-dir grpcio protobuf

COPY e2e/gen /app/gen
RUN groupadd -g 1000 e2e && useradd -m -u 1000 -g 1000 e2e

WORKDIR /app

COPY e2e/tests/workspace_common /app/workspace_common
COPY e2e/tests/10-llm-agent-loop/llm_providers.py /app/llm_providers.py
COPY e2e/tests/10-llm-agent-loop/test_llm_agent_loop.py /app/test_llm_agent_loop.py

USER 1000

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

CMD ["python", "/app/test_llm_agent_loop.py"]

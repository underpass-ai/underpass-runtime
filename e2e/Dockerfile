# Unified E2E test runner — all workspace tests in a single image.
# Select test via TEST_ID env var (e.g., TEST_ID=01, TEST_ID=16).
FROM docker.io/library/python:3.13-slim

WORKDIR /app
ENV PYTHONPATH=/app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir grpcio protobuf nats-py==2.9.0 redis==5.2.1

# Shared harness + generated proto stubs.
COPY e2e/tests/workspace_common /app/workspace_common
COPY e2e/gen                    /app/gen

# Copy every test script into /app/tests/{id}/
COPY e2e/tests/01-health/test_health.py                                     /app/tests/01/test.py
COPY e2e/tests/02-session-lifecycle/test_session_lifecycle.py                /app/tests/02/test.py
COPY e2e/tests/03-tool-discovery/test_tool_discovery.py                     /app/tests/03/test.py
COPY e2e/tests/04-recommendations/test_recommendations.py                   /app/tests/04/test.py
COPY e2e/tests/05-invoke-basic/test_invoke_basic.py                         /app/tests/05/test.py
COPY e2e/tests/06-invoke-policy/test_invoke_policy.py                       /app/tests/06/test.py
COPY e2e/tests/07-invocation-retrieval/test_invocation_retrieval.py         /app/tests/07/test.py
COPY e2e/tests/08-data-flow/test_data_flow.py                              /app/tests/08/test.py
COPY e2e/tests/10-llm-agent-loop/test_llm_agent_loop.py                    /app/tests/10/test.py
COPY e2e/tests/10-llm-agent-loop/llm_providers.py                          /app/tests/10/llm_providers.py
COPY e2e/tests/12-event-driven-agent/test_event_driven_agent.py             /app/tests/12/test.py
COPY e2e/tests/13-multi-agent-pipeline/test_multi_agent_pipeline.py         /app/tests/13/test.py
COPY e2e/tests/14-full-infra-stack/test_full_infra.py                       /app/tests/14/test.py
COPY e2e/tests/15-vllm-learning-loop/test_vllm_learning_loop.py            /app/tests/15/test.py
COPY e2e/tests/16-recommendation-evidence/test_recommendation_evidence.py   /app/tests/16/test.py
COPY e2e/tests/18-policy-driven-recommendations/test_policy_driven_recommendations.py /app/tests/18/test.py
COPY e2e/tests/19-neural-ts-recommendations/test_neural_ts_recommendations.py /app/tests/19/test.py
COPY e2e/tests/21-agent-feedback-loop/test_feedback.py                        /app/tests/21/test.py

RUN groupadd -r testuser && useradd -r -m -g testuser -u 1000 testuser && \
    chown -R testuser:testuser /app

USER testuser

# Entrypoint: python /app/tests/${TEST_ID}/test.py
CMD ["sh", "-c", "exec python /app/tests/${TEST_ID}/test.py"]

FROM docker.io/library/golang:1.25-bookworm

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    build-essential \
    coreutils \
    curl \
    findutils \
    gawk \
    grep \
    jq \
    less \
    openssh-client \
    patch \
    procps \
    ripgrep \
    sed \
    unzip \
    xz-utils \
    python3 \
    python3-pip \
    python3-pytest \
    python3-venv \
    python-is-python3 \
    ca-certificates \
    git \
    && rm -rf /var/lib/apt/lists/*

COPY services/workspace /tmp/workspace-src
RUN cd /tmp/workspace-src && \
    CGO_ENABLED=0 go build -o /usr/local/bin/workspace-service ./cmd/workspace && \
    rm -rf /tmp/workspace-src

COPY e2e/tests/16-workspace-vllm-go-todo-evolution /app/e2e/tests/16-workspace-vllm-go-todo-evolution

RUN groupadd -r testuser && useradd -r -m -g testuser -u 1000 testuser && \
    chown -R testuser:testuser /app
USER testuser

CMD ["python3", "/app/e2e/tests/16-workspace-vllm-go-todo-evolution/test_workspace_vllm_go_todo_evolution.py"]

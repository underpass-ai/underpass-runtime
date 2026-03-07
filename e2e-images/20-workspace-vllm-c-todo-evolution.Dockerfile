FROM docker.io/library/golang:1.25-bookworm

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    build-essential \
    ca-certificates \
    clang \
    cmake \
    coreutils \
    curl \
    findutils \
    gawk \
    git \
    grep \
    jq \
    less \
    openssh-client \
    patch \
    pkg-config \
    procps \
    python-is-python3 \
    python3 \
    python3-pip \
    python3-pytest \
    python3-venv \
    ripgrep \
    sed \
    unzip \
    xz-utils \
    && rm -rf /var/lib/apt/lists/*

COPY services/workspace /tmp/workspace-src
WORKDIR /tmp/workspace-src
RUN CGO_ENABLED=0 go build -o /usr/local/bin/workspace-service ./cmd/workspace
WORKDIR /app
RUN rm -rf /tmp/workspace-src

COPY e2e/tests/auxiliary/workspace_vllm_todo_evolution_generic.py /app/e2e/tests/auxiliary/workspace_vllm_todo_evolution_generic.py
COPY e2e/tests/20-workspace-vllm-c-todo-evolution /app/e2e/tests/20-workspace-vllm-c-todo-evolution

RUN groupadd -r testuser && useradd -r -m -g testuser -u 1000 testuser && \
    chown -R testuser:testuser /app
USER testuser

CMD ["python3", "/app/e2e/tests/auxiliary/workspace_vllm_todo_evolution_generic.py"]

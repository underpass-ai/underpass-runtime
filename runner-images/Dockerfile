# syntax=docker/dockerfile:1.7

FROM docker.io/library/debian:bookworm-slim AS base
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
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
    procps \
    ripgrep \
    sed \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd -g 1000 runner && \
    useradd -m -u 1000 -g 1000 -s /bin/bash runner && \
    mkdir -p /workspace/repo && \
    chown -R runner:runner /workspace
WORKDIR /workspace/repo
USER runner
CMD ["sh", "-lc", "sleep infinity"]

FROM docker.io/library/golang:1.25-bookworm AS golang

FROM docker.io/aquasec/trivy:0.56.2 AS trivybin

FROM docker.io/anchore/syft:v1.17.0 AS syftbin

FROM docker.io/library/debian:bookworm-slim AS toolchains
ENV DEBIAN_FRONTEND=noninteractive
ENV RUSTUP_HOME=/usr/local/rustup
ENV CARGO_HOME=/usr/local/cargo
ENV PATH=/usr/local/go/bin:/usr/local/cargo/bin:${PATH}
ARG RUST_TOOLCHAIN=stable
COPY --from=golang /usr/local/go /usr/local/go
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    build-essential \
    ca-certificates \
    clang \
    coreutils \
    curl \
    findutils \
    gawk \
    git \
    grep \
    jq \
    less \
    nodejs \
    npm \
    openssh-client \
    patch \
    procps \
    python-is-python3 \
    python3 \
    python3-pip \
    python3-pytest \
    python3-venv \
    ripgrep \
    sed \
    && rm -rf /var/lib/apt/lists/*
RUN curl https://sh.rustup.rs -sSf | sh -s -- -y --profile minimal --default-toolchain ${RUST_TOOLCHAIN} && \
    rustup component add rustfmt clippy rust-src
RUN groupadd -g 1000 runner && \
    useradd -m -u 1000 -g 1000 -s /bin/bash runner && \
    mkdir -p /workspace/repo && \
    chown -R runner:runner /workspace
WORKDIR /workspace/repo
USER runner
CMD ["sh", "-lc", "sleep infinity"]

FROM golang AS k6builder
RUN GOBIN=/tmp/bin go install go.k6.io/k6@v0.54.0

FROM base AS k6
USER root
COPY --from=k6builder /tmp/bin/k6 /usr/local/bin/k6
RUN chmod 0755 /usr/local/bin/k6
USER runner

FROM base AS secops
USER root
COPY --from=trivybin /usr/local/bin/trivy /usr/local/bin/trivy
COPY --from=syftbin /syft /usr/local/bin/syft
RUN chmod 0755 /usr/local/bin/trivy /usr/local/bin/syft
USER runner

FROM base AS container
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
    buildah \
    fuse-overlayfs \
    iptables \
    podman \
    skopeo \
    slirp4netns \
    uidmap \
    && rm -rf /var/lib/apt/lists/*
USER runner

FROM toolchains AS fat
USER root
COPY --from=trivybin /usr/local/bin/trivy /usr/local/bin/trivy
COPY --from=syftbin /syft /usr/local/bin/syft
COPY --from=k6builder /tmp/bin/k6 /usr/local/bin/k6
RUN chmod 0755 /usr/local/bin/trivy /usr/local/bin/syft /usr/local/bin/k6 && \
    apt-get update && apt-get install -y --no-install-recommends \
    buildah \
    fuse-overlayfs \
    iptables \
    podman \
    skopeo \
    slirp4netns \
    uidmap \
    && rm -rf /var/lib/apt/lists/*
USER runner

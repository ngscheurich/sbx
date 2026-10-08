FROM debian:unstable-slim

ARG MISE_VERSION=2026.9.18
ARG PI_VERSION=1.1.0

ENV LANG=C.UTF-8

# Install system packages
RUN apt-get update && \
  apt-get install --yes --no-install-recommends \
    curl ca-certificates git nodejs npm fd-find ripgrep gcc libc6-dev && \
  rm -rf /var/lib/apt/lists/*

# Install microsandbox
RUN curl -fsSL https://install.microsandbox.dev | sh

# Install mise-en-place
ENV PATH="/root/.local/bin:/root/.local/share/mise/shims:${PATH}" \
    MISE_TRUSTED_CONFIG_PATHS="/workspace"
RUN set -eux; \
    curl -fsSL https://mise.run | MISE_VERSION="$MISE_VERSION" sh; \
    mise --version && \
    echo 'eval "$(mise activate bash)"' >> ~/.bashrc

# Install project tooling
COPY mise.toml ./mise.toml
RUN set -eux; \
    mise install; \
    go version; \
    rm mise.toml

# Install Pi coding agent
RUN set -eux; \
    npm install -g --ignore-scripts @earendil-works/pi-coding-agent@"$PI_VERSION"; \
    pi --version

RUN set -eux; \
    pi install npm:@tintinweb/pi-subagents; \
    npm cache clean --force

CMD ["bash"]

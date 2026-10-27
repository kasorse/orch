# The orchestrator image for CI and containers (specs/delivery/SPEC.md §11.4). No image is
# published; build it yourself:
#
#   docker build -t orchestrator .
#   docker run --rm -v "$PWD:/work" -w /work orchestrator validate

# --- build: the orchestrator binary from the checked-out commit
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/orchestrator ./cmd/orchestrator

# --- runtime: git, bwrap, Node.js and the claude CLI, as a non-root user
FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends bubblewrap ca-certificates curl git gnupg \
 && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && apt-get purge -y --auto-remove gnupg \
 && rm -rf /var/lib/apt/lists/*

# The claude CLI, pinned. The orchestrator refuses a version outside the verified range of the
# claude adapter (internal/provider/claude/version.go); a test fails when this default leaves it.
# The auto-updater is off: a run must not change the CLI under its own manifest.
ARG CLAUDE_CODE_VERSION=2.1.287
RUN npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
 && claude --version
ENV DISABLE_AUTOUPDATER=1

COPY --from=build /out/orchestrator /usr/local/bin/orchestrator

RUN useradd --create-home --uid 10001 --shell /bin/bash orchestrator
USER orchestrator
WORKDIR /home/orchestrator

ENTRYPOINT ["orchestrator"]

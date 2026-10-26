# The benchmark's container (specs/benchmark/tasks/TASK-BM13-container.md): config A's shell and
# every model-written line of code run here and not on the owner's machine. Start it only with
# bench/docker/run.sh, which builds it from the committed tree and passes in nothing but the
# claude credentials, the tasks, the results and a cache volume.
#
#   bench/docker/run.sh build

# --- build: orchestrator (root module) and orchbench (bench/ module) from the checked-out commit
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/orchestrator ./cmd/orchestrator \
 && cd bench \
 && CGO_ENABLED=0 go build -trimpath -o /out/orchbench ./cmd/orchbench

# --- runtime: git, bwrap, Go, Python, Node and the claude CLI, as a non-root user
FROM node:22-bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends bubblewrap ca-certificates git python3 python3-pip python3-venv \
 && rm -rf /var/lib/apt/lists/*

COPY --from=build /usr/local/go /usr/local/go

# The claude CLI, pinned. orchbench and the orchestrator refuse any version outside the verified
# range >=2.1.283, <2.2.0 (MVP.md §41.4); the check below fails the build before an image outside
# it can exist. Raising the pin inside the range is an edit of this line only. The auto-updater
# is off: a run must not change the CLI under its own manifest.
ARG CLAUDE_VERSION=2.1.287
RUN npm install -g "@anthropic-ai/claude-code@${CLAUDE_VERSION}" \
 && claude --version | grep -E '^2\.1\.(28[3-9]|29[0-9]|[3-9][0-9]{2}|[0-9]{4,})([^0-9.]|$)'
ENV DISABLE_AUTOUPDATER=1

# graphify for config E, pinned: its setup builds the code graph (`graphify update .`, AST only,
# no network) and its implement, investigate and review read it through graphify-mcp, which needs
# the mcp extra.
ARG GRAPHIFY_VERSION=0.9.26
RUN python3 -m venv /opt/graphify \
 && /opt/graphify/bin/pip install --no-cache-dir "graphifyy[mcp]==${GRAPHIFY_VERSION}" \
 && ln -s /opt/graphify/bin/graphify /opt/graphify/bin/graphify-mcp /usr/local/bin/

COPY --from=build /out/orchestrator /out/orchbench /usr/local/bin/

# The commit the binaries were built from: `orchestrator version` does not name one yet (TODO P9).
ARG GIT_COMMIT=unknown
LABEL org.opencontainers.image.revision="${GIT_COMMIT}"
RUN echo "${GIT_COMMIT}" > /etc/orchbench-commit

# orchbench finds its configs in ./bench/configs; the B config rendered for this image is there
# for `orchestrator --config /opt/orche/rendered/B.yaml validate`. Its test binds are the two
# directories below, which exist and are not under the cache volume (a volume that already exists
# does not get what the image has at its mount point).
WORKDIR /opt/orche
COPY bench/configs bench/configs
COPY bench/docker/B.container.yaml rendered/B.yaml
RUN mkdir -p sandbox/ro sandbox/rw && chmod 0755 sandbox/ro && chmod 0777 sandbox/rw

# The user is whoever run.sh passes with --user (the owner's uid, so that the results directory
# is theirs), never root; none of them has an entry in /etc/passwd, so $HOME is set here and
# is writable by any uid. Tools that look the current user up (git's ident, Node's
# os.userInfo()) may fail without a passwd entry; the operator checks confirm that git, claude
# and orchbench start as such a user (TASK-BM13, "As built").
#
# The cache directory is a mount point: a new named volume copies its mode. orchbench's cache
# (mirrors, workspaces, validated.json) is ~/.cache/orchbench in it.
ENV HOME=/home/bench \
    GOPATH=/home/bench/.cache/go \
    PATH=/usr/local/go/bin:/home/bench/.cache/go/bin:/usr/local/bin:/usr/bin:/bin
RUN mkdir -p /home/bench/.cache /tasks /results \
 && chmod 0777 /home/bench /home/bench/.cache /tasks /results
USER 10001:10001
CMD ["orchbench", "--help"]

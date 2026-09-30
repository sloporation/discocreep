# syntax=docker/dockerfile:1.7

# --- builder ------------------------------------------------------------
# Compile the watcher, worker and API. Pinned to alpine for a small toolchain image; the
# resulting binary is static (CGO_ENABLED=0) so the runtime stage doesn't
# need a libc.
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Download modules in their own layer so source-only changes don't
# re-download dependencies. go.sum pins every module hash.
COPY go/go.mod go/go.sum ./
RUN go mod download

COPY go/ ./

# -trimpath strips local filesystem paths from the binary; -s -w drops the
# DWARF debug info and symbol table for a smaller image. GOOS/GOARCH are
# inferred from the build platform, which BuildKit fills in correctly for
# multi-arch builds.
# All binaries go in the same image; docker-compose picks which one each
# service runs.
# VERSION is the release tag (set by the release workflow); local builds say "dev".
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags="-s -w -X gitlab.com/jacxb/bots/bxt/go/internal/version.Version=${VERSION}" \
        -o /out/ \
        ./cmd/watcher ./cmd/worker ./cmd/api

# --- runtime ------------------------------------------------------------
# distroless/static is ~2MB, has no shell, and ships ca-certificates +
# tzdata. It's signed and built by Google; the :nonroot variant runs as
# uid/gid 65532 by default.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/watcher /usr/local/bin/watcher
COPY --from=builder /out/worker /usr/local/bin/worker
COPY --from=builder /out/api /usr/local/bin/api

# Both read their config from BXT_* env vars (or an optional config.yaml
# mounted into the container; see docker-compose.yml for the volume hook).
# The image runs a worker by default; the watcher service overrides this.
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/worker"]

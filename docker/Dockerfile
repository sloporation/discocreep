# syntax=docker/dockerfile:1.7

# --- builder ------------------------------------------------------------
# Compile the Go bot. Pinned to alpine for a small toolchain image; the
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
RUN CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/bxt \
        ./cmd/bxt

# --- runtime ------------------------------------------------------------
# distroless/static is ~2MB, has no shell, and ships ca-certificates +
# tzdata. It's signed and built by Google; the :nonroot variant runs as
# uid/gid 65532 by default.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/bxt /usr/local/bin/bxt

# The bot reads its config from BXT_* env vars (or an optional config.yaml
# mounted into the container; see docker-compose.yml for the volume hook).
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/bxt"]

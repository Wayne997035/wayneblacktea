# Stage 1: Build the React/Vite frontend
FROM node:22-alpine AS frontend

WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build the Go binary (with embedded frontend)
FROM golang:1.26.4-alpine AS builder

# Build identity for internal/buildinfo (GTD 61838147 — the MCP server's
# serverInfo.version and the wayneblacktea://system/build-info resource read
# these at runtime). Defaults are the SAME sentinels internal/buildinfo.go
# itself defaults to, so `docker build .` with no --build-arg produces an
# identical, obviously-unreleased binary — verified: an unmodified `docker
# build` here embeds "dev"/"none"/"unknown", never a value that could pass
# for a real version. The deploying pipeline (Railway or any CI) overrides
# these via `docker build --build-arg VERSION=... --build-arg COMMIT=...
# --build-arg BUILD_DATE=...`.
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

# Railway wiring. Railway injects its own variables into the build ONLY for
# names the Dockerfile declares with ARG, in the stage that needs them
# (https://docs.railway.com/builds/dockerfiles: "you must specify them in the
# Dockerfile using the ARG command" … "declare your environment variables in
# the stage they are required in"). RAILWAY_GIT_COMMIT_SHA is a Railway-
# provided variable, populated when the deploy originated from the GitHub
# trigger. The empty default keeps a plain `docker build` byte-identical to
# before: with nothing injected this stays empty, the branch below is skipped,
# and the binary still embeds dev/none/unknown.
ARG RAILWAY_GIT_COMMIT_SHA=

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Copy the built frontend into the path expected by //go:embed in cmd/server/main.go
COPY --from=frontend /web/dist ./cmd/server/web/dist
# Precedence: an explicit --build-arg always wins; Railway's injected SHA is
# only consulted when COMMIT was left at its sentinel. Date is derived in the
# same branch, so it moves together with Commit and never claims a build time
# for a build whose commit is unknown. Version is deliberately NOT derived —
# only a goreleaser tagged release sets a real version, a commit SHA must not
# masquerade as one.
#
# The hex assertion is load-bearing, not decoration. This value is interpolated
# into the -ldflags string, and go build re-splits that string on whitespace —
# so a value containing whitespace becomes ADDITIONAL linker flags rather than
# part of the commit. A second -X for the same symbol wins, which means an
# unvalidated value can silently rewrite buildinfo.Version into something that
# looks like a real release. That is the exact failure internal/buildinfo exists
# to prevent (see its doc comment: a plausible-looking fake version is worse
# than an obviously-fake one). Verified by PoC: shell metacharacters do NOT
# execute here (ARGs reach RUN as env vars and "${VAR}" expansion does not
# re-parse them) and the payload can never become a separate `go build`
# argument, so -toolexec is unreachable — the only real primitive is the extra
# -X, and this guard closes it. Fails the build closed on purpose: a blocked
# deploy is loud, a forged build identity is silent, and the running service is
# unaffected either way because Railway keeps the last good deployment.
RUN set -eu; \
    COMMIT_VAL="${COMMIT}"; \
    DATE_VAL="${BUILD_DATE}"; \
    if [ "${COMMIT_VAL}" = "none" ] && [ -n "${RAILWAY_GIT_COMMIT_SHA}" ]; then \
        case "${RAILWAY_GIT_COMMIT_SHA}" in \
            *[!0-9a-fA-F]*) echo "refusing: RAILWAY_GIT_COMMIT_SHA is not a hex sha" >&2; exit 1 ;; \
        esac; \
        COMMIT_VAL="${RAILWAY_GIT_COMMIT_SHA}"; \
        if [ "${DATE_VAL}" = "unknown" ]; then \
            DATE_VAL="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
        fi; \
    fi; \
    echo "build identity: version=${VERSION} commit=${COMMIT_VAL} date=${DATE_VAL}"; \
    CGO_ENABLED=0 go build -ldflags "-X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Version=${VERSION} \
    -X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Commit=${COMMIT_VAL} \
    -X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Date=${DATE_VAL}" \
    -o /app/server ./cmd/server/

# Stage 3: Minimal runtime image
FROM alpine:3.21

RUN apk --no-cache add ca-certificates && \
    addgroup -S appgroup && \
    adduser -S appuser -G appgroup

WORKDIR /app
COPY --from=builder /app/server .

USER appuser
EXPOSE 8420

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8420/health || exit 1

ENTRYPOINT ["/app/server"]

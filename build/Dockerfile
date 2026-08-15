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
# --build-arg BUILD_DATE=...`; wiring Railway itself to pass real values is a
# follow-up (this Dockerfile only had to prove the mechanism — see the PR
# report for why railway.toml wasn't touched here).
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Copy the built frontend into the path expected by //go:embed in cmd/server/main.go
COPY --from=frontend /web/dist ./cmd/server/web/dist
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Version=${VERSION} \
    -X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Commit=${COMMIT} \
    -X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Date=${BUILD_DATE}" \
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

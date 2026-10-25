# Stage 1: Build frontend
FROM oven/bun:1 AS frontend
WORKDIR /app/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ .
ARG VITE_SENTRY_DSN
ARG VITE_ENVIRONMENT
RUN bun run build

# Stage 2: Build Go binary
FROM golang:1.26 AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/dist web/dist
ARG VERSION=docker
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /wl ./cmd/wl

# Stage 3: Minimal runtime
FROM gcr.io/distroless/static-debian13
COPY --from=backend /wl /wl
ENTRYPOINT ["/wl"]
CMD ["serve", "--hosted"]

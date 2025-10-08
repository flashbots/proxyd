FROM golang:1.23.6-alpine3.20 AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

RUN apk add make jq git gcc musl-dev linux-headers

WORKDIR /app

# Copy dependency files first for better caching
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source code
COPY . .

# Build with cache mounts for faster compilation
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    make proxyd

FROM alpine:3.20

RUN apk add bind-tools jq curl bash git redis

COPY entrypoint.sh /bin/entrypoint.sh

RUN apk update && \
    apk add ca-certificates && \
    chmod +x /bin/entrypoint.sh

EXPOSE 8080

VOLUME /etc/proxyd

COPY --from=builder /app/bin/proxyd /bin/proxyd

ENTRYPOINT ["/bin/entrypoint.sh"]
CMD ["/bin/proxyd", "/etc/proxyd/proxyd.toml"]

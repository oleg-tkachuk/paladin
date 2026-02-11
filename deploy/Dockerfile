# syntax=docker/dockerfile:1
FROM golang:1.25 AS builder

ARG APP_VERSION
ARG GIT_COMMIT_HASH
ARG BUILD_TIME
ARG HTTP_PORT
ARG GRPC_PORT

ARG PROTOC_GEN_GO_VERSION=latest
ARG PROTOC_GEN_GO_GRPC_VERSION=latest

ENV APP_VERSION=${APP_VERSION:-undefined}
ENV GIT_COMMIT_HASH=${GIT_COMMIT_HASH:-undefined}
ENV BUILD_TIME=${BUILD_TIME:-undefined}

ENV GOCACHE=/root/.cache/go-build
ENV GOMODCACHE=/go/pkg/mod

RUN rm -f /etc/apt/apt.conf.d/docker-clean; echo 'Binary::apt::APT::Keep-Downloaded-Packages "true";' > /etc/apt/apt.conf.d/keep-cache

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
  --mount=type=cache,target=/var/lib/apt,sharing=locked \
  apt-get update && \
  apt-get install -y protobuf-compiler && \
  apt autoremove && \
  apt clean

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  go install google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION} && \
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION} && \
  go install github.com/vektra/mockery/v3@latest

WORKDIR /app

COPY ./go.mod ./go.mod
COPY ./go.sum ./go.sum
RUN --mount=type=cache,target=/go/pkg/mod \
  go mod download -x

COPY ./cmd ./cmd
COPY ./internal ./internal
COPY ./configs ./configs
COPY ./migrations ./migrations
COPY ./api ./api
COPY ./proto ./proto

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  go generate ./...

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  cd ./cmd/server && \
  CGO_ENABLED=0 go build -trimpath -tags=go_json -ldflags="-w -s \
  -X 'main.version=${APP_VERSION}' \
  -X 'main.commit=${GIT_COMMIT_HASH}' \
  -X 'main.buildTime=${BUILD_TIME}'" \
  -o /app/paladin .

FROM gcr.io/distroless/static:nonroot

ARG HTTP_PORT
ARG GRPC_PORT

WORKDIR /app
COPY --from=builder --chown=nonroot:nonroot /app/paladin /app/paladin
COPY --from=builder --chown=nonroot:nonroot /app/migrations /app/migrations
COPY --from=builder --chown=nonroot:nonroot /app/configs /app/configs

EXPOSE ${HTTP_PORT}
EXPOSE ${GRPC_PORT}

USER nonroot:nonroot
CMD ["/app/paladin"]

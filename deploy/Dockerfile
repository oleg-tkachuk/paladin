# syntax=docker/dockerfile:1
FROM golang:1.25 AS builder

ARG APP_VERSION
ARG GIT_COMMIT_HASH
ARG BUILD_TIME
ARG HTTP_PORT
ARG GRPC_PORT

ENV APP_VERSION=${APP_VERSION:-undefined}
ENV GIT_COMMIT_HASH=${GIT_COMMIT_HASH:-undefined}
ENV BUILD_TIME=${BUILD_TIME:-undefined}

WORKDIR /app

COPY ./go.mod ./go.mod
COPY ./go.sum ./go.sum
RUN go mod download

COPY ./cmd ./cmd
COPY ./internal ./internal
COPY ./configs ./configs
COPY ./migrations ./migrations

RUN cd ./cmd/server &&     CGO_ENABLED=0 go build -trimpath -ldflags="-w -s       -X 'main.version=${APP_VERSION}'       -X 'main.commit=${GIT_COMMIT_HASH}'       -X 'main.buildTime=${BUILD_TIME}'"     -o /app/paladin .

FROM gcr.io/distroless/static:nonroot

ARG HTTP_PORT
ARG GRPC_PORT

WORKDIR /app
COPY --from=builder --chown=nonroot:nonroot /app/paladin /app/paladin
COPY --from=builder --chown=nonroot:nonroot /app/migrations /app/migrations

EXPOSE ${HTTP_PORT}
EXPOSE ${GRPC_PORT}

USER nonroot:nonroot
CMD ["/app/paladin"]

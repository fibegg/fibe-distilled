# syntax=docker/dockerfile:1

FROM golang:1.26.4-alpine3.24 AS builder

RUN apk add --no-cache build-base git
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,id=fibe-distilled-go-mod,target=/go/pkg/mod,sharing=locked go mod download
COPY . .
RUN --mount=type=cache,id=fibe-distilled-go-build,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=1 go build -tags sqlite_omit_load_extension -o /out/fibe-distilled ./cmd/fibe-distilled

FROM alpine:3.24

RUN apk add --no-cache ca-certificates docker-cli docker-cli-compose git && \
    mkdir -p /app/data /opt/fibe
WORKDIR /app
COPY --from=builder /out/fibe-distilled /usr/local/bin/fibe-distilled
EXPOSE 2402
CMD ["fibe-distilled"]

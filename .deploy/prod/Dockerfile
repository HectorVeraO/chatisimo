# Release image for the production stack (.deploy/prod/docker-compose.yaml).
FROM golang:1.27.3-alpine3.24 AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/chatisimo ./cmd/chatisimo

FROM alpine:3.24.2
LABEL org.opencontainers.image.source="https://github.com/HectorVeraO/chatisimo"
COPY --from=build /out/chatisimo /usr/local/bin/chatisimo
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/chatisimo"]
CMD ["run"]

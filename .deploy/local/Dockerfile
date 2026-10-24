# Test-only images for the local e2e profile; the release image is built separately (S4).
FROM golang:1.27.3-alpine3.24 AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -cover -coverpkg=./... -o /out/chatisimo ./cmd/chatisimo \
 && go build -o /out/fakediscord ./cmd/fakediscord

FROM alpine:3.24.2 AS fakediscord
COPY --from=build /out/fakediscord /usr/local/bin/fakediscord
USER 65532:65532
EXPOSE 10010
ENTRYPOINT ["/usr/local/bin/fakediscord"]
CMD ["--listen", "0.0.0.0:10010"]

# Runs as root so it can write coverage files into the bind-mounted /cover on any host.
FROM alpine:3.24.2 AS chatisimo-cover
COPY --from=build /out/chatisimo /usr/local/bin/chatisimo
ENTRYPOINT ["/usr/local/bin/chatisimo"]

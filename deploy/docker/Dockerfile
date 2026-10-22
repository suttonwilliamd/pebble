FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/pebbled ./cmd/pebbled
COPY internal/snapshot ./internal/snapshot
COPY internal/syncproto ./internal/syncproto
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pebbled ./cmd/pebbled \
    && mkdir -p /out/data && chown 65532:65532 /out/data

FROM scratch
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build /out/pebbled /pebbled
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/pebbled", "-healthcheck", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/pebbled"]
CMD ["-data", "/data", "-listen", ":8080", "-token-file", "/run/secrets/pebble-token"]

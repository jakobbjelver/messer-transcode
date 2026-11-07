# syntax=docker/dockerfile:1
# The server image is the orchestrator and UI only: it never mounts the media
# library and never runs ffmpeg. Encoding happens on the host, where
# VideoToolbox lives.
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w \
      -X github.com/jakobbjelver/messer-transcode/internal/version.Version=${VERSION} \
      -X github.com/jakobbjelver/messer-transcode/internal/version.Commit=${COMMIT} \
      -X github.com/jakobbjelver/messer-transcode/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/messer-transcode ./cmd/messer-transcode

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/messer-transcode /usr/local/bin/messer-transcode

USER nonroot:nonroot
EXPOSE 8099

# The orchestrator. It reads /config/config.json and keeps its state next to it,
# so the whole service is one bind mount and a backup of that mount is a backup
# of the queue.
ENTRYPOINT ["/usr/local/bin/messer-transcode"]
CMD ["serve", "--config", "/config/config.json", "--db", "/data/state.db", "--token-file", "/config/server.token", "--listen", ":8099"]

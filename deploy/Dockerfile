# syntax=docker/dockerfile:1
# The server image is the orchestrator and UI only: it never mounts the media
# library and never runs ffmpeg. Encoding happens on the host, where
# VideoToolbox lives.
#
# The React app is embedded into the Go binary, so it is built first and its dist
# is injected: `go build` fails without it (`pattern all:app/dist: no matching
# files`), which is why this stage exists rather than a separate asset volume.
FROM --platform=$BUILDPLATFORM node:22-alpine AS app

WORKDIR /src/web/app
COPY web/app/package.json web/app/package-lock.json ./
RUN npm ci
COPY web/app/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=app /src/web/app/dist ./web/app/dist

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
CMD ["serve", "--config", "/config/config.json", "--db", "/config/state.db", "--token-file", "/config/server.token", "--listen", ":8099"]

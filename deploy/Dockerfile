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

# Until the server subcommand lands (M3) the default is the version banner, so
# the image is runnable and honest rather than exiting on an unknown command.
ENTRYPOINT ["/usr/local/bin/messer-transcode"]
CMD ["version"]

FROM golang:1.27-bookworm AS build

RUN apt-get update \
    && apt-get install -y --no-install-recommends libvips-dev pkg-config \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/s3-image-resizer ./cmd/server

FROM debian:bookworm-slim

# libvips42 は libheif / libwebp / libjpeg / libpng / libgif を含む
RUN apt-get update \
    && apt-get install -y --no-install-recommends libvips42 ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/s3-image-resizer /usr/local/bin/s3-image-resizer

USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/s3-image-resizer"]

# Local test image only; never an installation/release image.
FROM node:22.22.2-bookworm@sha256:62e4daa6819762bbd3072af77cc282ab72c631c4aed30dd7980192babaf385b3 AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
COPY data/api/ /src/data/api/
RUN npm run build && node scripts/embed.mjs

FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea
ENV GOTOOLCHAIN=local CGO_ENABLED=1 GOWORK=off
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ffmpeg ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN curl --fail --location --retry 3 --max-time 180 \
      https://github.com/AlexxIT/go2rtc/releases/download/v1.9.15/go2rtc_linux_arm64 -o /usr/local/bin/go2rtc \
    && echo 'af416f22b21a24ab83b8d78642190b4932ae695b4e386853f11d1b0319781763  /usr/local/bin/go2rtc' | sha256sum -c - \
    && chmod 0755 /usr/local/bin/go2rtc
RUN curl --fail --location --retry 3 --max-time 180 \
      https://github.com/golangci/golangci-lint/releases/download/v2.14.0/golangci-lint-2.14.0-linux-arm64.tar.gz -o /tmp/lint.tar.gz \
    && echo 'ee7ec5f3453d15ddf106fae5a4d6c71737712348a979d1fe9cd52ec7ea299bae  /tmp/lint.tar.gz' | sha256sum -c - \
    && tar -xzf /tmp/lint.tar.gz -C /tmp \
    && mv /tmp/golangci-lint-2.14.0-linux-arm64/golangci-lint /usr/local/bin/ \
    && rm -rf /tmp/lint.tar.gz /tmp/golangci-lint-2.14.0-linux-arm64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY config/ config/
COPY deploy/ deploy/
COPY internal/ internal/
COPY pkg/ pkg/
COPY data/ data/
COPY --from=web /src/data/web/ data/web/
COPY .golangci.yml .golangci-lint-version ./
# The source snapshot intentionally excludes .git. Both roots are /src here;
# change only path resolution in the image, preserving every maintained rule.
RUN sed -i 's/relative-path-mode: gitroot/relative-path-mode: cfg/' .golangci.yml
RUN test "$(cat .golangci-lint-version)" = v2.14.0 \
    && go test ./config ./internal/app ./internal/transport/cli ./internal/viewer \
    && go vet ./internal/app/... \
    && golangci-lint run ./config/... ./internal/app/... ./internal/transport/cli/... \
    && go test -c -o /fixture/app.test ./internal/app
RUN ffmpeg -hide_banner -loglevel error \
      -f lavfi -i testsrc2=size=640x360:rate=15 \
      -t 4 -an -c:v libx264 -pix_fmt yuv420p -preset ultrafast -tune zerolatency -g 15 /fixture/clip.mkv \
    && ffmpeg -hide_banner -loglevel error -f lavfi -i testsrc2=size=320x240:rate=10 -t 2 -an -c:v mpeg4 /fixture/unsupported.mkv \
    && { go version; golangci-lint version; go2rtc -version; ffmpeg -version; dpkg-query -W ffmpeg; \
         sha256sum /fixture/app.test /fixture/clip.mkv /usr/local/bin/go2rtc; } > /fixture/versions.txt
COPY scripts/controlled-media/origin.yaml scripts/controlled-media/other.yaml /fixture/
WORKDIR /src/internal/app
ENTRYPOINT ["/fixture/app.test"]

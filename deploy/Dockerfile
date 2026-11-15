# Build context: extracted scripts/package.sh release directory. No source/Node.
FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS trust
FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc
COPY --from=trust /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY bin/ /opt/camstream/current/bin/
COPY licenses/ /usr/share/doc/camstream/licenses/
COPY BUILDINFO /usr/share/doc/camstream/BUILDINFO
WORKDIR /var/lib/camstream
USER 10001:10001
ENTRYPOINT ["/opt/camstream/current/bin/camstream"]
CMD ["serve", "--config", "/etc/camstream/config.yaml"]

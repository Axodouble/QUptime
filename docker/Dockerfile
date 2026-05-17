# syntax=docker/dockerfile:1.7

# Build stage. Runs on the runner's native arch (BUILDPLATFORM) and
# cross-compiles the Go binary for whichever target the manifest list
# is being assembled for (TARGETOS/TARGETARCH). Keeps multi-arch
# builds fast — only the final link is per-arch, the Go toolchain is
# always native.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

# Module cache layer — re-uses unless go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
  go build \
  -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o /out/qu \
  ./cmd/qu

# Runtime stage. distroless/static has CA roots for HTTPS probes and
# nothing else — no shell, no package manager. Runs as root so the
# daemon can open ICMP sockets and write under /etc/quptime; operators
# can override at deploy time with `docker run --user`.
FROM gcr.io/distroless/static-debian12:latest

COPY --from=builder /out/qu /usr/local/bin/qu

ENV QUPTIME_DIR=/etc/quptime
VOLUME ["/etc/quptime"]
EXPOSE 9901

ENTRYPOINT ["/usr/local/bin/qu"]
CMD ["serve"]

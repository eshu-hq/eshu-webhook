# Standalone webhook satellite image: pure-Go binary, built reproducibly
# with -trimpath. Stateless: no volumes, no migrations (238/239 stay
# core-side); the satellite only verifies, normalizes, and submits to the
# versioned core intake API.
FROM golang:1.26.10-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308 AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
# EKS nodes are linux/amd64; pin GOARCH so builds on any host (e.g. arm64
# dev boxes) produce a runnable binary.
RUN CGO_ENABLED=0 GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/webhook ./cmd/webhook

# Base digest resolved 2026-10-28; re-pin when bumping the base.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/webhook /usr/local/bin/webhook
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/webhook"]

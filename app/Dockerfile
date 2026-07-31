# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src

# Dependencies land on their own layer: editing application code below must
# not invalidate the module cache. `go mod download` (not `go mod tidy`) plus
# a committed go.sum is what makes this build reproducible and verifiable —
# tidy would happily resolve a different version than the one you tested.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .

ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X main.buildVersion=${VERSION}" \
        -o /out/server .

# distroless/static holds no shell, no package manager and no libc — there is
# essentially nothing in here to exploit. It works because CGO is disabled
# above, so the binary is fully static.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/server /server

USER nonroot:nonroot
EXPOSE 8080

# The image has no curl or wget, so the binary probes itself.
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/server", "-healthcheck"]

ENTRYPOINT ["/server"]

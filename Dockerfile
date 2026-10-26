# One image for every Go binary (api, migrate, seed; later worker, mockprovider),
# plus a `test` stage used by compose.test.yml. Base images are pinned by digest
# (docs/PLAN.md §6 #16). No secret is ever passed as a build ARG: the API key
# reaches containers only at run time (compose env_file), and .dockerignore keeps
# .env out of the build context.

ARG GO_IMAGE=golang:1.27-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

FROM ${GO_IMAGE} AS source
WORKDIR /src
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
# Modules stay in the image layer: the test stage runs `go test` offline at run time.
RUN go mod download
COPY . .

FROM source AS build
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

# Test runner: Go toolchain + sources; tests run with the race detector (needs cgo).
# Binaries and the race-enabled build cache are baked in so the run itself starts fast.
FROM source AS test
RUN go build -o /usr/local/bin/ ./cmd/... && go test -race -count=1 -run '^$' ./... >/dev/null
CMD ["scripts/test-container.sh"]

FROM ${RUNTIME_IMAGE} AS runtime
COPY --from=build /out/ /usr/local/bin/
USER nonroot:nonroot
EXPOSE 8080
CMD ["api"]

# Build the manager binary
FROM docker.io/golang:1.24 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy local operatortrace-go module so the replace directive works
COPY operatortrace-go/ /operatortrace-go/

# Copy the Go Modules manifests
COPY example/sample-operator/go.mod go.mod
COPY example/sample-operator/go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY example/sample-operator/cmd/main.go cmd/main.go
COPY example/sample-operator/api/ api/
COPY example/sample-operator/internal/ internal/
# Copy operatortrace-go sources for inclusion in the final image
COPY operatortrace-go/ /workspace/operatortrace-go/

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /operatortrace-go/ /operatortrace-go/
USER 65532:65532

ENTRYPOINT ["/manager"]

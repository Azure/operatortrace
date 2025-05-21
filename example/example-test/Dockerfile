# Use a multi-stage build to ensure GLIBC compatibility
FROM golang:1.24-bullseye AS builder

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the Go app
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o emit-spans main.go

# Use the same base image family for the runtime environment
FROM debian:bullseye-slim

# Copy the pre-built binary from the previous stage
COPY --from=builder /app/emit-spans /usr/local/bin/emit-spans

# Command to run the binary
CMD ["emit-spans"]
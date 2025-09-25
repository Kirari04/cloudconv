# Stage 1: Build the Go application
# We use a specific version of the Go Alpine image for reproducible builds.
FROM golang:1.24-alpine AS builder

# Set necessary environment variables
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

# Install build dependencies (git for go modules, gcc for CGO if needed, though disabled)
RUN apk add --no-cache git build-base

# Set the working directory inside the container
WORKDIR /app

# Copy go module files and download dependencies. This is done first to leverage Docker's layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source code
COPY . .

# Build the application, creating a statically linked binary.
RUN go build -ldflags="-w -s" -o /go/bin/server .

# Stage 2: Create the final, minimal production image
# We use a specific, stable version of Alpine.
FROM alpine:3.22

# Install runtime dependencies - only ffmpeg is needed.
RUN apk add --no-cache ffmpeg

# Create a dedicated, non-root user and group for the application
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Set the working directory
WORKDIR /app

# Copy the compiled binary from the builder stage
COPY --from=builder /go/bin/server .

# Copy the templates directory required by the application
COPY templates ./templates

# Create the directories for uploads and converted files
RUN mkdir -p /app/uploads /app/converted

# Change the ownership of the app directory to the new non-root user
# This ensures our application can write to the uploads and converted directories.
RUN chown -R appuser:appgroup /app

# Switch to the non-root user
USER appuser

# Expose the port the application runs on
EXPOSE 3000

# Set the command to run the application
CMD ["./server"]

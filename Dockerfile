# Use an official Golang image as the base image
FROM golang:1.23-alpine

# Set environment variables for cross-compilation
ENV GO111MODULE=on \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

# Set working directory inside container
WORKDIR /app

# Copy go.mod and go.sum files first (for dependency caching)
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the rest of the application code
COPY . .

# Build the Go application into an executable binary named "main"
RUN go build -o main .

# Expose port 8080 to the outside world
EXPOSE 8080

# Command to run the executable binary
CMD ["./main"]

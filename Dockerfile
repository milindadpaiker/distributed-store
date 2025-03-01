FROM golang:latest

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Install Air
RUN go install github.com/air-verse/air@latest


# Copy the source code
COPY . .

# Command to run air
CMD ["air"]

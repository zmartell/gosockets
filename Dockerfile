# Use official golang image as the base image
FROM golang:latest

# Set working directory
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application1
RUN go build -o main main.go

# Expose port 8080
EXPOSE 8080

# Command to run the executable
CMD ["./main"]

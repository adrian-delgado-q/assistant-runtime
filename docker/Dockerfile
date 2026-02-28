FROM golang:1.22-alpine AS builder
WORKDIR /app
RUN apk add --no-cache gcc musl-dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o clearoutspaces-api ./cmd/api/main.go

FROM alpine:3.19
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata wget
ENV TZ=America/Toronto
COPY --from=builder /app/clearoutspaces-api .
COPY templates/ templates/
EXPOSE 8080
CMD ["./clearoutspaces-api"]

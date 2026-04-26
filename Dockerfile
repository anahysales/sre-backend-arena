FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o api cmd/api/main.go

FROM alpine:latest

WORKDIR /root/

COPY --from=builder /app/api .

EXPOSE 8081

CMD ["./api"]
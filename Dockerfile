FROM golang:1.24-alpine as builder
WORKDIR /app
COPY go.* ./
RUN go mod download
COPY . .
RUN go build -o server .

FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/server .
CMD ["./server"]
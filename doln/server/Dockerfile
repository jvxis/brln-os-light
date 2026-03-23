FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build
COPY . .
RUN GOTOOLCHAIN=local go mod tidy && CGO_ENABLED=0 go build -o /doln-server .

FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /doln-server /usr/local/bin/doln-server

ENTRYPOINT ["doln-server"]

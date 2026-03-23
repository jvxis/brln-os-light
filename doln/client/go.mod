module github.com/pagcoin/doln-client

go 1.23

toolchain go1.24.0

require (
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/lightningnetwork/lnd v0.18.5-beta
	github.com/miekg/dns v1.1.62
	google.golang.org/grpc v1.67.1
	gopkg.in/macaroon.v2 v2.1.0
)

replace google.golang.org/protobuf => github.com/lightninglabs/protobuf-go-hex-display v1.30.0-hex-display

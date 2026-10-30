`stateservice.proto` is copied unchanged from Lightning Network Daemon
`v0.21.4-beta`, SHA256
`e7c9e1083804c5510874a4e549e3a26864e91892c830921e82d555e469f97242`.
The upstream license is in `LND_LICENSE`.

The State stubs were generated, without manual edits, with protoc 3.21.12,
protoc-gen-go v1.36.11 and protoc-gen-go-grpc v1.6.2:

```sh
protoc --go_out=. --go_opt=paths=source_relative \
  --go_opt=Mstateservice.proto=lightningos-light/lnrpc \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --go-grpc_opt=Mstateservice.proto=lightningos-light/lnrpc stateservice.proto
```

This adds fresh wallet/server readiness probes for configuration apply/rollback.
Existing generated service files are unchanged.

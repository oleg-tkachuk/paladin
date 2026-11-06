module github.com/oleg-tkachuk/paladin/sdk/go

go 1.27.0

toolchain go1.27.3

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2
	buf.build/go/protovalidate v1.4.0
	connectrpc.com/connect/v2 v2.0.2
	github.com/oleg-tkachuk/limes v1.0.0-rc.49
	github.com/oleg-tkachuk/limes/biscuit v0.1.30
	github.com/spiffe/go-spiffe/v2 v2.9.0
	google.golang.org/genproto v0.0.0-20261026165753-2ffab32bce7e
	google.golang.org/genproto/googleapis/api v0.0.0-20261026165753-2ffab32bce7e
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261026165753-2ffab32bce7e
	google.golang.org/protobuf v1.36.12
)

require (
	cel.dev/cel-go v0.33.1 // indirect
	cel.dev/expr v0.25.3 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/eclipse-biscuit/biscuit-go/v2 v2.2.2 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20261014143641-f0251eb4aa3e // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

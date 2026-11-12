module github.com/oleg-tkachuk/paladin/sdk/go/internal/oteltest

go 1.27.0

toolchain go1.27.4

require (
	connectrpc.com/otelconnect v0.12.0
	github.com/oleg-tkachuk/paladin/sdk/go v0.145.4
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0
	go.opentelemetry.io/otel v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2 // indirect
	buf.build/go/protovalidate v1.4.0 // indirect
	cel.dev/cel-go v0.33.2 // indirect
	cel.dev/expr v0.25.3 // indirect
	connectrpc.com/connect v1.21.1 // indirect
	connectrpc.com/connect/v2 v2.0.2 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/eclipse-biscuit/biscuit-go/v2 v2.2.2 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/oleg-tkachuk/limes v1.0.0-rc.65 // indirect
	github.com/oleg-tkachuk/limes/biscuit v0.1.32 // indirect
	github.com/spiffe/go-spiffe/v2 v2.9.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.yaml.in/yaml/v3 v3.0.6 // indirect
	golang.org/x/exp v0.0.0-20261106192419-47ce03d25ef0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto v0.0.0-20261110000943-09e21b237f2c // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20261110000943-09e21b237f2c // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261110000943-09e21b237f2c // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

// The SDK under test is the one in this tree, never a published version.
replace github.com/oleg-tkachuk/paladin/sdk/go => ../..

package grpcapi

//go:generate protoc --go_out=.. --go_opt=module=github.com/oleg-tkachuk/paladin --go-grpc_out=.. --go-grpc_opt=module=github.com/oleg-tkachuk/paladin --connect-go_out=.. --connect-go_opt=module=github.com/oleg-tkachuk/paladin paladin.proto
//go:generate protoc --go_out=.. --go_opt=module=github.com/oleg-tkachuk/paladin --go-grpc_out=.. --go-grpc_opt=module=github.com/oleg-tkachuk/paladin --connect-go_out=.. --connect-go_opt=module=github.com/oleg-tkachuk/paladin public/v1/paladin.proto

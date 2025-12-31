package grpcapi

import (
	"context"

	"google.golang.org/grpc"
)

type PaladinServer interface {
	CreateObject(context.Context, *CreateObjectRequest) (*CreateObjectResponse, error)
	GetObject(context.Context, *GetObjectRequest) (*GetObjectResponse, error)
	CompleteObject(context.Context, *CompleteObjectRequest) (*CompleteObjectResponse, error)

	InitiateMultipart(context.Context, *InitiateMultipartRequest) (*InitiateMultipartResponse, error)
	SignPart(context.Context, *SignPartRequest) (*SignPartResponse, error)
	CompleteMultipart(context.Context, *CompleteMultipartRequest) (*CompleteMultipartResponse, error)
	AbortMultipart(context.Context, *AbortMultipartRequest) (*AbortMultipartResponse, error)
}

func RegisterPaladinServer(s *grpc.Server, srv PaladinServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "paladin.v1.Paladin",
		HandlerType: (*PaladinServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "CreateObject", Handler: _CreateObject_Handler},
			{MethodName: "GetObject", Handler: _GetObject_Handler},
			{MethodName: "CompleteObject", Handler: _CompleteObject_Handler},
			{MethodName: "InitiateMultipart", Handler: _InitiateMultipart_Handler},
			{MethodName: "SignPart", Handler: _SignPart_Handler},
			{MethodName: "CompleteMultipart", Handler: _CompleteMultipart_Handler},
			{MethodName: "AbortMultipart", Handler: _AbortMultipart_Handler},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "paladin.proto",
	}, srv)
}

func _CreateObject_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateObjectRequest)
	if err := dec(in); err != nil {
		return nil, err
	}

	if interceptor == nil {
		return srv.(PaladinServer).CreateObject(ctx, in)
	}

	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/paladin.v1.Paladin/CreateObject"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PaladinServer).CreateObject(ctx, req.(*CreateObjectRequest))
	}

	return interceptor(ctx, in, info, handler)
}

func _GetObject_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetObjectRequest)
	if err := dec(in); err != nil {
		return nil, err
	}

	if interceptor == nil {
		return srv.(PaladinServer).GetObject(ctx, in)
	}

	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/paladin.v1.Paladin/GetObject"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PaladinServer).GetObject(ctx, req.(*GetObjectRequest))
	}

	return interceptor(ctx, in, info, handler)
}

func _CompleteObject_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CompleteObjectRequest)
	if err := dec(in); err != nil {
		return nil, err
	}

	if interceptor == nil {
		return srv.(PaladinServer).CompleteObject(ctx, in)
	}

	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/paladin.v1.Paladin/CompleteObject"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PaladinServer).CompleteObject(ctx, req.(*CompleteObjectRequest))
	}

	return interceptor(ctx, in, info, handler)
}

func _InitiateMultipart_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(InitiateMultipartRequest)
	if err := dec(in); err != nil {
		return nil, err
	}

	if interceptor == nil {
		return srv.(PaladinServer).InitiateMultipart(ctx, in)
	}

	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/paladin.v1.Paladin/InitiateMultipart"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PaladinServer).InitiateMultipart(ctx, req.(*InitiateMultipartRequest))
	}

	return interceptor(ctx, in, info, handler)
}

func _SignPart_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SignPartRequest)
	if err := dec(in); err != nil {
		return nil, err
	}

	if interceptor == nil {
		return srv.(PaladinServer).SignPart(ctx, in)
	}

	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/paladin.v1.Paladin/SignPart"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PaladinServer).SignPart(ctx, req.(*SignPartRequest))
	}

	return interceptor(ctx, in, info, handler)
}

func _CompleteMultipart_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CompleteMultipartRequest)
	if err := dec(in); err != nil {
		return nil, err
	}

	if interceptor == nil {
		return srv.(PaladinServer).CompleteMultipart(ctx, in)
	}

	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/paladin.v1.Paladin/CompleteMultipart"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PaladinServer).CompleteMultipart(ctx, req.(*CompleteMultipartRequest))
	}

	return interceptor(ctx, in, info, handler)
}

func _AbortMultipart_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(AbortMultipartRequest)
	if err := dec(in); err != nil {
		return nil, err
	}

	if interceptor == nil {
		return srv.(PaladinServer).AbortMultipart(ctx, in)
	}

	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/paladin.v1.Paladin/AbortMultipart"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PaladinServer).AbortMultipart(ctx, req.(*AbortMultipartRequest))
	}

	return interceptor(ctx, in, info, handler)
}

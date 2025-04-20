package grpc

import (
	"context"
	"net"

	pb "service-gateway/proto"
	"service-gateway/redis"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// grpc 서비스의 인터페이시 내장 구조체 생성
type GatewayServer struct {
	pb.UnimplementedGatewayServiceServer
}

func StartServer(lis net.Listener) {
	s := grpc.NewServer()

	// gRPC gatewayServer 서비스 등록 부분
	pb.RegisterGatewayServiceServer(s, &GatewayServer{})

	// postman test를 위한 로직
	reflection.Register(s)

	s.Serve(lis)
}

// gRPC 통해 호출되는 핸들러 메소드
/**
req = 클라이언트 호출 데이터
ctx = 메타데이터
redis 저장 후 응답 반환
*/
func (s *GatewayServer) SaveSession(ctx context.Context, req *pb.SessionRequest) (*pb.GenericResponse, error) {
	err := redis.SaveSession(ctx, req.Key, req.Value)
	if err != nil {
		return &pb.GenericResponse{Success: false, Message: err.Error()}, nil
	}
	return &pb.GenericResponse{Success: true, Message: "Saved"}, nil
}

func (s *GatewayServer) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloResponse, error) {
	name := req.GetName()
	if name == "" {
		name = "World"
	}

	message := "Hello, " + name + "!"
	return &pb.HelloResponse{Message: message}, nil
}

// 세션 조회
func (s *GatewayServer) GetSession(ctx context.Context, req *pb.SessionRequest) (*pb.SessionResponse, error) {
	value, err := redis.GetSession(ctx, req.Key)
	if err != nil {
		return &pb.SessionResponse{Value: ""}, err
	}
	return &pb.SessionResponse{Value: value}, nil
}

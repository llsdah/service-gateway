package grpc

import (
	"context"
	"net"

	pb "service-gateway/proto"
	"service-gateway/redis"

	"google.golang.org/grpc"
)

// grpc 서비스의 인터페이시 내장 구조체 생성
type GatewayServer struct {
	pb.UnimplementedGatewayServiceServer
}

func StartServer(lis net.Listener) {
	s := grpc.NewServer()

	// gRPC gatewayServer 서비스 등록 부분
	pb.RegisterGatewayServiceServer(s, &GatewayServer{})

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

package grpc

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"service-gateway/kafka"
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

func (s *GatewayServer) SaveSession(ctx context.Context, req *pb.SaveSessionRequest) (*pb.GenericResponse, error) {
	err := redis.SaveSession(ctx, req.Key, req.Value, time.Duration(req.Ttl)*time.Second)

	if err != nil {
		return &pb.GenericResponse{Success: false, Message: err.Error()}, nil
	}
	return &pb.GenericResponse{Success: true, Message: "Saved"}, nil
}

func (s *GatewayServer) GetSession(ctx context.Context, req *pb.GetSessionRequest) (*pb.GenericResponse, error) {
	value, err := redis.GetSession(ctx, req.Key)
	if err != nil {
		return &pb.GenericResponse{Success: false, Message: err.Error()}, err
	}
	return &pb.GenericResponse{Success: true, Message: value}, nil
}

// ip port 용도
func (s *GatewayServer) SaveSessionWithTarget(ctx context.Context, req *pb.SaveSessionWithTargetRequest) (*pb.GenericResponse, error) {
	err := redis.SaveSessionWithTarget(ctx, req.Ip, req.Port, req.Key, req.Value, time.Duration(req.Ttl)*time.Second)

	if err != nil {
		return &pb.GenericResponse{Success: false, Message: err.Error()}, nil
	}
	return &pb.GenericResponse{Success: true, Message: "Saved"}, nil
}

func (s *GatewayServer) GetSessionWithTarget(ctx context.Context, req *pb.GetSessionWithTargetRequest) (*pb.GenericResponse, error) {
	value, err := redis.GetSessionWithTarget(ctx, req.Ip, req.Port, req.Key)

	if err != nil {
		return &pb.GenericResponse{Success: false, Message: err.Error()}, err
	}
	return &pb.GenericResponse{Success: true, Message: value}, nil
}

// Kafka
func (s *GatewayServer) SendKafkaMessage(ctx context.Context, req *pb.SendKafkaMessageRequest) (*pb.GenericResponse, error) {
	brokers := req.GetBrokers()
	topic := req.GetTopic()
	key := req.GetKey()
	value := req.GetValue()

	if len(brokers) == 0 {
		return &pb.GenericResponse{
			Success: false,
			Message: "no brokers provided",
		}, nil
	}

	if topic == "" {
		return &pb.GenericResponse{
			Success: false,
			Message: "topic is required",
		}, nil
	}

	err := kafka.ProduceToTopicWithBrokers(ctx, brokers, topic, key, value)

	if err != nil {
		log.Printf("kafka 전송 실패 : %v", err)
		return &pb.GenericResponse{
			Success: false,
			Message: fmt.Sprintf("kafka 전송 실패 : %v", err),
		}, err
	}
	return &pb.GenericResponse{
		Success: true,
		Message: fmt.Sprintf("kafka sned, topic : %v", topic),
	}, nil
}

func (s *GatewayServer) SendFileToKafka(ctx context.Context, req *pb.SendFileRequest) (*pb.SendFileResponse, error) {
	inputPath := req.GetInputPath()
	outputPath := req.GetOutputPath()
	topic := req.GetTopic()
	brokers := req.GetBrokers()

	in, err := os.Open(inputPath)

	if err != nil {
		return &pb.SendFileResponse{
			Success: false,
			Message: fmt.Sprintf("입력 파일 열기 실패 : %v", err),
		}, nil
	}

	scanner := bufio.NewScanner(in)
	lines := []string{}

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	in.Close()

	if err := scanner.Err(); err != nil {
		return &pb.SendFileResponse{
			Success: false,
			Message: fmt.Sprintf("파일 읽기 오류 : %v", err),
		}, nil
	}

	out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)

	if err != nil {
		return &pb.SendFileResponse{
			Success: false,
			Message: fmt.Sprintf("출력파일 생성 실패 : %v", err),
		}, nil
	}

	defer out.Close()

	writer := bufio.NewWriter(out)
	lineCount := 0

	for _, line := range lines {
		_, err := writer.WriteString(line + "\n")
		if err != nil {
			return &pb.SendFileResponse{
				Success: false,
				Message: fmt.Sprintf("출력 파일 쓰기 실패 : %v", err),
			}, nil
		}
		// kafka 전송
		err = kafka.ProduceToTopicWithBrokers(ctx, brokers, topic, fmt.Sprintf("%d", time.Now().UnixNano()), line)
		if err != nil {
			log.Printf("kafka 전송 실패: %v", err)
			return &pb.SendFileResponse{
				Success: false,
				Message: fmt.Sprintf("kafka 전송 실패 : %v", err),
			}, nil
		}
		lineCount++

	}

	writer.Flush()

	// 원본 파일 내용 제거 (읽은 내용만큼)
	err = os.Truncate(inputPath, 0)
	if err != nil {
		return &pb.SendFileResponse{
			Success: false,
			Message: fmt.Sprintf("입력 파일 비우기 실패 : %v", err),
		}, nil
	}

	return &pb.SendFileResponse{
		Success: true,
		Message: fmt.Sprintf("파일 처리 및 Kafka 전송 완료, line count : %v", lineCount),
	}, nil

}

func (s *GatewayServer) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloResponse, error) {
	name := req.GetName()
	if name == "" {
		name = "World"
	}

	message := "Hello, " + name + "!"
	return &pb.HelloResponse{Message: message}, nil
}

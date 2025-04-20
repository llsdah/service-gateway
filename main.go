package main

import (
	"log"
	"net"
	"service-gateway/grpc"
)

func main() {
	// grpc 서버 오픈 준비
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)

	}
	log.Println("Service Gateway listening on :50051")
	grpc.StartServer(lis)
}

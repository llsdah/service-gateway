package main

import (
	"log"
	"net"
	"service-gateway/grpc"
	"service-gateway/handlers"

	"github.com/gin-gonic/gin"
)

func main() {

	// go routine
	go func() {
		// grpc 서버 오픈 준비
		lis, err := net.Listen("tcp", ":50051")
		if err != nil {
			log.Fatalf("failed to listen: %v", err)

		}
		log.Println("Service Gateway listening on :50051")
		grpc.StartServer(lis)
	}()

	// HTTP 통신
	r := gin.Default()

	r.GET("/v1/hello", handlers.HelloGetHandler)
	r.POST("/v1/session/set", handlers.SetSession)
	r.Run(":8090")

}

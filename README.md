# service-gateway

proto 설치 위해 
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# 터미널에서 proto 디렉토리에 위치하고
protoc --go_out=. --go-grpc_out=. gateway.proto

+ protoc 설치 및 환경변수 등록
root 디렉토리에서 아래 명령어 수행
protoc --go_out=paths=source_relative:. --go-grpc_out=paths=source_relative:. proto/gateway.proto


다음 질문

Go로 작성 중이고, gRPC 서버는 proto까지 완성한 상태야.
이제 클라이언트에서 Redis 세션을 저장하는 요청을 보내고 싶은데,
pb.NewGatewayServiceClient(...) 호출 예시랑 요청 메시지 구성은 어떻게 하면 좋을까?
proto에는 SaveSession(SessionRequest) rpc가 있어.


4/21
레디스 적재, 조회 0 
카프카 적재 0, 파일로그 읽어서 적재 및 백업 

카프카 적재 방식 고민, 파일 사용? 혹은 호출 

레디스 설정으로 뺴서 연결 필요 


4/25
도커 기동방법 
docker build --no-cache -t service-gateway:0.0.1 .

docker run -p 50051:50051 -e REDIS_HOST=host.docker.internal:6379 -e KAFKA_BROKERS=host.docker.internal:9092 service-gateway:0.0.1




// 고정 토픽
func ProduceMessage(ctx context.Context, key, value string) error {
	msg := kafka.Message{
		Key:   []byte(key),
		Value: []byte(value),
	}

	// kafka 메시지 전송 produce 객체 설정
	writer := &kafka.Writer{
		Addr:     kafka.TCP("localhost:9092"), // 브로커 주소
		Topic:    "gateway-events",            // 기본메시지 토픽
		Balancer: &kafka.LeastBytes{},         // 로드밸런싱 방식
	}

	// 비동기 전송
	err := writer.WriteMessages(ctx, msg)
	if err != nil {
		log.Printf("Kafka produce error: %v", err)
	}
	// 에러시 로그 찍고 레어 리턴
	return err
}


docker run -d  -p 8082:8080 -p 50002:50000 --name jenkins --user root  -v /var/run/docker.sock:/var/run/docker.sock  -v jenkins_home:/var/jenkins_home jenkins/jenkins

docker 설치 
apt-get update
apt-get install -y apt-transport-https ca-certificates curl software-properties-common
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | apt-key add -
add-apt-repository "deb [arch=amd64] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable"
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io

apt-get install -y nano
apt-get install -y vim
echo "deb [arch=amd64] https://download.docker.com/linux/ubuntu focal stable" | tee /etc/apt/sources.list.d/docker.list




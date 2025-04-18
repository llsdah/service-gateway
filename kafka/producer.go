package kafka

import (
	"context"
	"log"

	"github.com/segmentio/kafka-go"
)

// kafka 메시지 전송 produce 객체 설정

var writer = &kafka.Writer{
	Addr:     kafka.TCP("localhost:9092"), // 브로커 주소
	Topic:    "gateway-events",            // 메시지 토픽
	Balancer: &kafka.LeastBytes{},         // 로드밸런싱 방식
}

// 메시지 만들고
func ProduceMessage(ctx context.Context, key, value string) error {
	msg := kafka.Message{
		Key:   []byte(key),
		Value: []byte(value),
	}
	// 비동기 전송
	err := writer.WriteMessages(ctx, msg)
	if err != nil {
		log.Printf("Kafka produce error: %v", err)
	}
	// 에러시 로그 찍고 레어 리턴
	return err
}

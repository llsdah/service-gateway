package kafka

import (
	"context"
	"log"

	"github.com/segmentio/kafka-go"
)

// kafka 메시지 전송 produce 객체 설정
var writer = &kafka.Writer{
	Addr:     kafka.TCP("localhost:9092"), // 브로커 주소
	Topic:    "gateway-events",            // 기본메시지 토픽
	Balancer: &kafka.LeastBytes{},         // 로드밸런싱 방식
}

// 고정 토픽
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

// 동적 토픽
func ProduceToTopicWithBrokers(ctx context.Context, brokers []string, topic, key, value string) error {

	var keyBytes []byte
	if key != "" {
		keyBytes = []byte(key)
	} else {
		keyBytes = nil
	}

	msg := kafka.Message{
		Key:   keyBytes,
		Value: []byte(value),
	}

	var err error

	attempt := 3 // 시도 회수

	for i := 0; i < attempt; i++ {

		writer := kafka.NewWriter(kafka.WriterConfig{
			Brokers:  brokers,
			Topic:    topic,
			Balancer: &kafka.LeastBytes{},
		})
		err = writer.WriteMessages(ctx, msg)
		writer.Close()

		if err == nil {
			break
		}
		log.Printf("kafka produce error ( attempt coiunt %d ) : %v", i, err)
	}

	return err

}

package kafka

import (
	"context"
	"log"
	"sync"

	"github.com/segmentio/kafka-go"
)

var writerMap map[string]*kafka.Writer // 각 브로커 보관
var writerMu sync.Mutex                // 쓰레드 관리 lock 사용

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

		log.Printf("brokers : %+v", brokers)
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

func initKafkaWriters(brokers []string, topic string) {
	writerMu.Lock()
	defer writerMu.Unlock()

	for _, broker := range brokers {
		if _, exists := writerMap[broker]; !exists {
			writer := getKafkaWriter(broker, topic)
			writerMap[broker] = writer
			log.Printf("Initialized writer for broker %s", broker)
		}
	}
}

func getKafkaWriter(broker string, topic string) *kafka.Writer {
	return &kafka.Writer{
		Addr:     kafka.TCP(broker),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
	}
}

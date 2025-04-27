package kafka

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
)

type KafkaWriterManager struct {
	writers map[string]*kafka.Writer
	mu      sync.RWMutex
}

var manager = &KafkaWriterManager{
	writers: make(map[string]*kafka.Writer),
}

// 내부키 생성 함수 brokers, topic
func generateKey(brokers []string, topic string) string {
	return strings.Join(brokers, ",") + ":" + topic
}

// Writer 가져오기 없으면 생성
func (m *KafkaWriterManager) GetWriter(brokers []string, topic string) *kafka.Writer {
	key := generateKey(brokers, topic)

	m.mu.RLock()
	writer, exists := m.writers[key]
	m.mu.RUnlock()

	if exists {
		return writer
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if writer, exists := m.writers[key]; exists {
		return writer
	}

	writer = &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
	}
	m.writers[key] = writer
	return writer
}

// writer 종료 용도
func (m *KafkaWriterManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, writer := range m.writers {
		_ = writer.Close()
	}
}

// 메시지를 보낼때 브로커, 토픽, 키, 값까지 외부 입력받아 전송
func (m *KafkaWriterManager) Produce(ctx context.Context, brokers []string, topic, key, value string) error {
	writer := m.GetWriter(brokers, topic)
	msg := kafka.Message{
		Key:   []byte(key),
		Value: []byte(value),
	}

	var err error
	// 재시도 횟수
	kafkaRepeatValue := 3
	for i := 0; i < kafkaRepeatValue; i++ {
		err = writer.WriteMessages(ctx, msg)
		if err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 100 * time.Microsecond) // 딜레이 부분 점차 증가
	}
	return err
}

// 외부 노출 용도
func GetManager() *KafkaWriterManager {
	return manager
}

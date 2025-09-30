package kafkax

import (
	"context"    // WriteMessages에 타임아웃을 적용하기 위해 필요
	"crypto/tls" // TLS 설정을 위해 필요
	"errors"     // 설정 검증 시 에러 리턴을 위해 필요
	"log"        // 장애 시 서버 다운 방지용 경고 로그용
	"sync"       // 안전한 종료 및 버퍼 처리 동기화를 위해 필요
	"time"       // 타임아웃/배치 시간 제어를 위해 필요

	"github.com/segmentio/kafka-go"            // Kafka 클라이언트
	"github.com/segmentio/kafka-go/sasl/plain" // SASL/PLAIN 메커니즘 사용
	"github.com/segmentio/kafka-go/sasl/scram" // SASL/SCRAM 메커니즘 사용
)

type Config struct {
	Enabled      bool          // 런타임에서 켜고 끌 수 있도록 분기
	Brokers      []string      // 브로커 리스트
	Topic        string        // ✅ 단일 토픽
	ClientID     string        // 브로커 모니터링/제한 정책을 위한 식별자
	Acks         string        // 내구성/지연 정책
	Compression  string        // 네트워크/스토리지 비용 최적화
	Timeout      time.Duration // 각 write 타임아웃
	BatchBytes   int64         // 배치 크기 상한
	BatchTimeout time.Duration // 배치 플러시 지연
	SASL         struct {
		Enabled   bool
		Mechanism string // "PLAIN"|"SCRAM-SHA-256"|"SCRAM-SHA-512"
		Username  string
		Password  string
	}
	TLS struct {
		Enabled            bool
		InsecureSkipVerify bool
	}
}

// Publisher: 호출 측에서 의존성 역전을 위해 인터페이스로 노출
type Publisher interface {
	Publish(key, value []byte) // 비동기로 넣고, 흐름 차단 방지
	Close() error              // 애플리케이션 종료 시 자원 정리
}

// noop: Kafka 꺼짐/미설정 환경에서도 앱이 동작하도록 보강
type noop struct{}

func (noop) Publish(key, value []byte) {}
func (noop) Close() error              { return nil }
func Noop() Publisher                  { return noop{} }

// 내부 전송 큐 타입: backpressure 없이 드롭 가능하도록 설계
type message struct {
	key []byte
	val []byte
}

type publisher struct {
	cfg    Config         // 런타임 파라미터 보관
	w      *kafka.Writer  // ✅ 단일 토픽용 Writer 하나만 유지(오버헤드 최소화)
	ch     chan message   // 비동기 버퍼 채널(요청 경로 차단 방지)
	wg     sync.WaitGroup // 안전한 종료를 위한 goroutine join
	closed chan struct{}  // 종료 시그널
}

// NewPublisher: Transport 기반 writer 생성 (kafka-go v0.4.49 호환)
func NewPublisher(cfg Config) (Publisher, error) {
	// 기능 비활성화 시 noop으로 대체 → 런타임 토글 편의성
	if !cfg.Enabled {
		return Noop(), nil
	}
	// 브로커/토픽은 필수 → 누락 시 즉시 에러로 초기화 실패 감지
	if len(cfg.Brokers) == 0 {
		return nil, errors.New("kafka brokers empty")
	}
	if cfg.Topic == "" {
		return nil, errors.New("kafka topic empty")
	}

	// ✅ v0.4.49: Dialer가 아닌 Transport에 TLS/SASL/Timeout/ClientID를 설정
	tr := &kafka.Transport{
		// DialTimeout: 브로커 접속/재시도 시 상한 시간을 두어 장애 전파 방지
		DialTimeout: 10 * time.Second,
		// ClientID: 브로커에서 클라이언트 소스 식별을 위해 설정
		ClientID: cfg.ClientID,
	}
	// TLS 설정: 네트워크 구간 암호화 필요 시 활성화
	if cfg.TLS.Enabled {
		tr.TLS = &tls.Config{InsecureSkipVerify: cfg.TLS.InsecureSkipVerify} // 사설 CA 환경 임시 허용
	}
	// SASL 설정: 보안 클러스터 접속 시 인증 메커니즘 적용
	if cfg.SASL.Enabled {
		switch cfg.SASL.Mechanism {
		case "PLAIN":
			tr.SASL = plain.Mechanism{Username: cfg.SASL.Username, Password: cfg.SASL.Password} // 단순 사용자/비밀번호 인증
		case "SCRAM-SHA-256":
			mech, _ := scram.Mechanism(scram.SHA256, cfg.SASL.Username, cfg.SASL.Password) // SCRAM 256으로 challenge/response 인증
			tr.SASL = mech
		case "SCRAM-SHA-512":
			mech, _ := scram.Mechanism(scram.SHA512, cfg.SASL.Username, cfg.SASL.Password) // SCRAM 512로 인증 강도 상향
			tr.SASL = mech
		}
	}

	// acks 매핑: 지연/내구성 요구에 따라 선택
	requiredAcks := kafka.RequireOne // leader ack(기본값 유사)로 지연과 내구성 균형
	switch cfg.Acks {
	case "none":
		requiredAcks = kafka.RequireNone // 최대 성능, 데이터 손실 허용
	case "all":
		requiredAcks = kafka.RequireAll // ISR 전원 ack, 지연↑ 내구성↑
	}

	// 압축 매핑: 네트워크/디스크 사용량 최적화
	var comp kafka.Compression
	switch cfg.Compression {
	case "gzip":
		comp = kafka.Gzip // 높은 압축률, CPU 비용↑
	case "snappy":
		comp = kafka.Snappy // 속도/압축률 균형
	case "lz4":
		comp = kafka.Lz4 // 매우 빠름, 압축률 보통
	case "zstd":
		comp = kafka.Zstd // 고압축률, 최근 브로커 권장
	default:
		comp = kafka.Snappy // 기본값: 안정적인 선택
	}

	// ✅ 단일 토픽 Writer 구성: 토픽별 캐시는 불필요하므로 하나만 유지
	w := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...), // 브로커 접속점 설정
		Topic:        cfg.Topic,                 // ✅ 모든 로그가 이 토픽으로 간다
		Balancer:     &kafka.LeastBytes{},       // 파티션 간 균등 분산(키 제공 시 파티셔너가 재정의)
		RequiredAcks: requiredAcks,              // 위에서 매핑한 내구성 정책 적용
		Compression:  comp,                      // 네트워크 비용 절감
		BatchBytes:   cfg.BatchBytes,            // 배치 크기 제한으로 메모리/지연 균형
		BatchTimeout: cfg.BatchTimeout,          // 배치 플러시 지연으로 처리량 향상
		Async:        false,                     // 쓰기 자체는 동기(batch단), 외부는 비동기 채널로 감싼다
		Transport:    tr,                        // ✅ v0.4.49 핵심: Dialer 대신 Transport 주입
		// AllowAutoTopicCreation: false,                  // 운영에서 토픽 사전생성 강제하려면 해제(기본 false)
	}

	p := &publisher{
		cfg:    cfg,                      // 종료/리뷰 시 설정 접근 필요
		w:      w,                        // 단일 writer
		ch:     make(chan message, 1000), // 버스트 트래픽 방어용 버퍼(가득 차면 드롭)
		closed: make(chan struct{}),      // 종료 신호 전달
	}
	// 백그라운드 루프 시작: 채널의 메시지를 실제 Kafka로 전송
	p.wg.Add(1)
	go p.loop()
	return p, nil
}

// loop: 백그라운드에서 채널을 소비하며 카프카에 쓰기
func (p *publisher) loop() {
	defer p.wg.Done() // 종료 대기 보장을 위해 필요
	for {
		select {
		case m, ok := <-p.ch: // 버퍼에서 메시지 수신
			if !ok {
				return
			} // 채널 닫힘: 정상 종료
			ctx, cancel := context.WithTimeout(context.Background(), p.cfg.Timeout) // write 상한 시간 부여
			err := p.w.WriteMessages(ctx, kafka.Message{
				Key:   m.key, // 동일 키(TCID 등)로 파티션 일관성 보장
				Value: m.val, // 직렬화된 로그 페이로드
			})
			cancel() // 리소스 누수 방지
			if err != nil {
				log.Printf("[kafka] write failed: %v", err) // 장애 시 서비스 흐름 차단 금지, 경고만 남김
			}
		case <-p.closed: // 종료 신호 수신
			return
		}
	}
}

// Publish: 요청 경로를 차단하지 않도록 채널에 넣고 가득 차면 드롭
func (p *publisher) Publish(key, value []byte) {
	select {
	case p.ch <- message{key: key, val: value}: // 평시: 비동기 큐 적재
	default:
		log.Printf("[kafka] buffer full, drop message") // 폭주 시: 드롭해 게이트웨이 지연 전파 차단
	}
}

// Close: graceful shutdown을 위해 goroutine 종료 및 writer 닫기
func (p *publisher) Close() error {
	close(p.closed)    // 루프 종료 신호
	close(p.ch)        // 생산자 종료
	p.wg.Wait()        // 루프가 남은 메시지 처리 후 종료되도록 대기
	return p.w.Close() // 네트워크 자원 정리
}

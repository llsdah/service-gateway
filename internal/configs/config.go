package config

import (
	"log"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Application struct {
		Name      string `yaml:"name"`
		GroupCode string `yaml:"group_code"`
		Log       struct {
			Topic   string `yaml:"topic"`
			Inbound struct {
				Request  string `yaml:"request"`
				Response string `yaml:"response"`
			} `yaml:"inbound"`
			Outbound struct {
				Request  string `yaml:"request"`
				Response string `yaml:"response"`
			} `yaml:"outbound"`
		} `yaml:"log"`
	}

	Server struct {
		Addr      string `yaml:"addr"`
		ReadTOms  int    `yaml:"read_timeout_ms"`
		WriteTOms int    `yaml:"write_timeout_ms"`
		IdleTOms  int    `yaml:"idle_timeout_ms"`
	} `yaml:"server"`

	Kafka KafkaConfig `yaml:"kafka"`

	// ★ 추가: gateway.yaml의 db 블록
	DB struct {
		Enabled  bool   `yaml:"enabled"`
		Driver   string `yaml:"driver"` // "mysql" (MariaDB)
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		Name     string `yaml:"name"`
	} `yaml:"db"`

	Hosts map[string]string `yaml:"hosts"`

	Routes []struct {
		Name  string `yaml:"name"`
		Match struct {
			PathPrefix  string   `yaml:"path_prefix"`
			PathPattern string   `yaml:"path_pattern"` // ✅ 추가
			Methods     []string `yaml:"methods"`
		} `yaml:"match"`
		Backend struct {
			Scheme      string `yaml:"scheme"`
			Host        string `yaml:"host"`
			Method      string `yaml:"method"`
			PathRewrite string `yaml:"path_rewrite"`
		} `yaml:"backend"`
		Options struct {
			RequireSession    bool `yaml:"require_session"`
			GenerateIfMissing bool `yaml:"generate_if_missing"`
		} `yaml:"options"`
	} `yaml:"routes"`

	Tracing struct {
		Enabled bool `yaml:"enabled"`
		OTLP    struct {
			Endpoint string `yaml:"endpoint"`
			Insecure bool   `yaml:"insecure"`
		}
	} `yaml:"tracing"`
}

type KafkaSASL struct {
	Enabled   bool   `yaml:"enabled"`
	Mechanism string `yaml:"mechanism"` // "PLAIN"|"SCRAM-SHA-256"|"SCRAM-SHA-512"
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
}
type KafkaTLS struct {
	Enabled            bool `yaml:"enabled"`
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
}
type KafkaConfig struct {
	Enabled        bool      `yaml:"enabled"`
	Brokers        []string  `yaml:"brokers"`
	ClientID       string    `yaml:"client_id"`
	Acks           string    `yaml:"acks"`
	Compression    string    `yaml:"compression"`
	TimeoutMs      int       `yaml:"timeout_ms"`
	BatchBytes     int64     `yaml:"batch_bytes"`
	BatchTimeoutMs int       `yaml:"batch_timeout_ms"`
	SASL           KafkaSASL `yaml:"sasl"`
	TLS            KafkaTLS  `yaml:"tls"`
}

var (
	AppConfig Config
	once      sync.Once
)

func LoadConfig(path string) {

	once.Do(func() {

		file, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("\u274c Failed to read config file: %v", err)
		}

		if err := yaml.Unmarshal(file, &AppConfig); err != nil {
			log.Fatalf("\u274c Failed to parse config YAML: %v", err)
		}

		//log.Printf("\u2705 Config loaded: GRPC=%d, HTTP=%d, Redis=%s", AppConfig.Application.GrpcPort, AppConfig.Application.HttpPort, AppConfig.Redis.Host)

	})
}

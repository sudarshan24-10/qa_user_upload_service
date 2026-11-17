package config

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
)

const (
	QueueBackendRedis    = "redis"
	QueueBackendRabbitMQ = "rabbitmq"
)

type Config struct {
	PostgresURL              string
	MongoURL                 string
	JWTSecret                string
	ObjectStorage_access_key string
	ObjectStorage_secret_key string
	MAX_SIZE                 int64
	BucketName               string
	MinioEndpoint            string
	QueueBackend             string
	RedisURL                 string
	RedisHost                string
	RedisPort                string
	RedisPassword            string
	RedisDB                  string
	RedisStream              string
	RedisConsumerGroup       string
	ProcessingTimeout        time.Duration
	WorkerPollInterval       time.Duration
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		log.Printf("invalid %s value %q: %v; using default %s", key, value, err, fallback)
		return fallback
	}

	return duration
}

// MinJWTSecretBytes is the shortest JWT_SECRET accepted: 32 bytes (256 bits), the
// size of the SHA-256 output used by HS256.
const MinJWTSecretBytes = 32

// validateJWTSecret checks the signing secret is long enough. It counts bytes (not
// characters), and its error text never contains the secret.
func validateJWTSecret(secret string) error {
	if len(secret) < MinJWTSecretBytes {
		return fmt.Errorf("JWT_SECRET must be at least %d bytes", MinJWTSecretBytes)
	}
	return nil
}

func Load() *Config {

	_ = godotenv.Load()

	cfg := &Config{
		PostgresURL:              os.Getenv("POSTGRES_URL"),
		MongoURL:                 os.Getenv("MONGO_URL"),
		JWTSecret:                os.Getenv("JWT_SECRET"),
		ObjectStorage_access_key: os.Getenv("OBJECT_STORAGE_ACCESS_KEY"),
		ObjectStorage_secret_key: os.Getenv("OBJECT_STORAGE_SECRET_KEY"),
		MAX_SIZE:                 25 * 1024 * 1024, // 25MB
		BucketName:               os.Getenv("BUCKET_NAME"),
		MinioEndpoint:            os.Getenv("MINIO_ENDPOINT"),
		QueueBackend:             os.Getenv("QUEUE_BACKEND"),
		RedisURL:                 os.Getenv("REDIS_URL"),
		RedisHost:                os.Getenv("REDIS_HOST"),
		RedisPort:                os.Getenv("REDIS_PORT"),
		RedisPassword:            os.Getenv("REDIS_PASSWORD"),
		RedisDB:                  os.Getenv("REDIS_DB"),
		RedisStream:              os.Getenv("REDIS_STREAM"),
		RedisConsumerGroup:       os.Getenv("REDIS_CONSUMER_GROUP"),
		ProcessingTimeout:        parseDurationEnv("PROCESSING_TIMEOUT", 5*time.Minute),
		WorkerPollInterval:       parseDurationEnv("WORKER_POLL_INTERVAL", 30*time.Second),
	}

	if cfg.PostgresURL == "" {
		log.Fatal("POSTGRES_URL is not set")
	}
	if cfg.MongoURL == "" {
		log.Fatal("MONGO_URL is not set")
	}
	if cfg.JWTSecret == "" {
		log.Fatal("JWT_SECRET is not set")
	}
	// An HS256 signing key shorter than 32 bytes is too easy to brute-force. Refuse to
	// start rather than run with weak tokens. Only the requirement is logged, never
	// the secret or its length.
	if err := validateJWTSecret(cfg.JWTSecret); err != nil {
		log.Fatal(err)
	}
	if cfg.QueueBackend == "" {
		log.Fatal("QUEUE_BACKEND is not set")
	}
	if cfg.QueueBackend != QueueBackendRedis && cfg.QueueBackend != QueueBackendRabbitMQ {
		log.Fatal("QUEUE_BACKEND must be either 'redis' or 'rabbitmq'")
	}

	if cfg.QueueBackend == QueueBackendRedis {
		if cfg.RedisURL == "" {
			log.Fatal("REDIS_URL is not set")
		}
		if cfg.RedisStream == "" {
			log.Fatal("REDIS_STREAM is not set")
		}
		if cfg.RedisConsumerGroup == "" {
			log.Fatal("REDIS_CONSUMER_GROUP is not set")
		}
	}

	return cfg
}

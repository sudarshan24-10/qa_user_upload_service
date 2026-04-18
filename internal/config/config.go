package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
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

	return cfg
}

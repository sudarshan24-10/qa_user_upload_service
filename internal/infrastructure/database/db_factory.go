package database

import (
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.mongodb.org/mongo-driver/mongo"
)

type Factory struct {
	PostgresDB *pgxpool.Pool
	MongoDB    *mongo.Client
}

func NewFactory(postgresURL, mongoURL string) (*Factory, error) {
	pg, err := initPostgres(postgresURL)
	if err != nil {
		log.Fatal(`Failed to connect to Postgres: `, err)
	}
	mongoClient, err := initMongo(mongoURL)
	if err != nil {
		log.Fatal(`Failed to connect to MongoDB: `, err)
	}
	return &Factory{
		PostgresDB: pg,
		MongoDB:    mongoClient,
	}, nil
}

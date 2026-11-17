package database

import (
	"context"
	"fmt"

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
		return nil, fmt.Errorf("failed to connect to Postgres: %w", err)
	}
	mongoClient, err := initMongo(mongoURL)
	if err != nil {
		pg.Close()
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	return &Factory{
		PostgresDB: pg,
		MongoDB:    mongoClient,
	}, nil
}

func (f *Factory) Close(ctx context.Context) error {
	f.PostgresDB.Close()
	return f.MongoDB.Disconnect(ctx)
}

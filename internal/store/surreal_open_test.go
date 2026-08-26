package store

import (
	"context"
	"errors"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	connectionhttp "github.com/surrealdb/surrealdb.go/pkg/connection/http"
)

type failingOpenConnection struct {
	connection.Connection
	failAt   string
	failErr  error
	closeErr error
	closed   bool
}

func (*failingOpenConnection) Connect(context.Context) error { return nil }

func (c *failingOpenConnection) Close(context.Context) error {
	c.closed = true
	return c.closeErr
}

func (c *failingOpenConnection) SignIn(context.Context, any) (string, error) {
	if c.failAt == "signin" {
		return "", c.failErr
	}
	return "", nil
}

func (c *failingOpenConnection) Use(context.Context, string, string) error {
	if c.failAt == "use" {
		return c.failErr
	}
	return nil
}

func TestOpenConnectedClosesConnectionOnInitializationFailure(t *testing.T) {
	initializationErr := errors.New("open initialization failed")
	closeConnectionErr := errors.New("close connection failed")
	for _, test := range []struct {
		name     string
		failAt   string
		closeErr error
	}{
		{name: "signin", failAt: "signin"},
		{name: "use", failAt: "use"},
		{name: "apply_schema", failAt: "apply_schema"},
		{name: "cleanup_error", failAt: "signin", closeErr: closeConnectionErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := &failingOpenConnection{
				Connection: connectionhttp.New(&connection.Config{}),
				failAt:     test.failAt,
				failErr:    initializationErr,
				closeErr:   test.closeErr,
			}
			db, err := surrealdb.FromConnection(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			_, err = openConnected(t.Context(), db, "user", "pass", "ns", "db")
			if err == nil {
				t.Fatalf("open error = %v", err)
			}
			if test.failAt != "apply_schema" && !errors.Is(err, initializationErr) {
				t.Fatalf("open error = %v; want initialization error", err)
			}
			if test.closeErr != nil && !errors.Is(err, test.closeErr) {
				t.Fatalf("open error = %v; want close error", err)
			}
			if !conn.closed {
				t.Fatal("failed connection was not closed")
			}
		})
	}
}

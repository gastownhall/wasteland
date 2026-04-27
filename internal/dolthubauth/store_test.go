package dolthubauth

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsUniqueViolation(t *testing.T) {
	t.Run("postgres unique violation", func(t *testing.T) {
		err := &pgconn.PgError{Code: "23505"}
		if !isUniqueViolation(err) {
			t.Fatal("expected unique violation to be detected")
		}
	})

	t.Run("wrapped postgres unique violation", func(t *testing.T) {
		err := errors.New("outer")
		wrapped := errors.Join(err, &pgconn.PgError{Code: "23505"})
		if !isUniqueViolation(wrapped) {
			t.Fatal("expected wrapped unique violation to be detected")
		}
	})

	t.Run("non unique postgres error", func(t *testing.T) {
		err := &pgconn.PgError{Code: "23503"}
		if isUniqueViolation(err) {
			t.Fatal("did not expect foreign-key violation to match")
		}
	})

	t.Run("non postgres error", func(t *testing.T) {
		if isUniqueViolation(errors.New("duplicate")) {
			t.Fatal("did not expect plain string error to match")
		}
	})
}

func TestConnectTokenExpired(t *testing.T) {
	expiresAt := time.Date(2026, 4, 25, 5, 30, 0, 0, time.UTC)

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "before expiry", now: expiresAt.Add(-time.Nanosecond), want: false},
		{name: "at expiry boundary", now: expiresAt, want: true},
		{name: "after expiry", now: expiresAt.Add(time.Nanosecond), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connectTokenExpired(tt.now, expiresAt); got != tt.want {
				t.Fatalf("connectTokenExpired(%s, %s) = %v, want %v", tt.now, expiresAt, got, tt.want)
			}
		})
	}
}

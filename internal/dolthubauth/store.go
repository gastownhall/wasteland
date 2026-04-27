package dolthubauth

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"
)

// AuthStore defines the persistence API used by the auth service.
type AuthStore interface {
	ReadinessChecker
	CreateConnectToken(context.Context, []byte, []byte, string, UserMetadata, time.Time, time.Time) error
	RedeemConnectToken(context.Context, RedeemInput) (*Connection, error)
	GetConnection(context.Context, string, string, string, string) (*Connection, error)
	GetConnectionCredential(context.Context, string, string, string, string, CredentialCipher) (*Connection, string, error)
	PatchRigHandle(context.Context, string, string, string, string, string, int, time.Time) (*Connection, error)
	UpsertWasteland(context.Context, string, string, string, string, WastelandConfig, int, time.Time) (*Connection, error)
	DeleteWasteland(context.Context, string, string, string, string, string, int, time.Time) (*Connection, error)
	PatchWastelandSettings(context.Context, string, string, string, string, string, string, bool, int, time.Time) (*Connection, error)
	UseServiceNonce(context.Context, string, string, time.Time, time.Time) error
}

// RedeemInput bundles the data needed to redeem a connect token.
type RedeemInput struct {
	TenantID           string
	Environment        string
	ConnectTokenMAC    []byte
	RedeemSecretMAC    []byte
	Metadata           UserMetadata
	APIKey             string
	Now                time.Time
	Cipher             CredentialCipher
	ValidateCredential func(context.Context, string) (ValidationErrorCode, error)
}

// CreateConnectToken stores a pending connect token in Postgres.
func (s *PostgresStore) CreateConnectToken(
	ctx context.Context,
	connectTokenMAC, redeemSecretMAC []byte,
	subjectID string,
	metadata UserMetadata,
	expiresAt, now time.Time,
) error {
	if err := s.reapExpiredConnectTokens(ctx, now.UTC()); err != nil {
		return fmt.Errorf("reap expired connect tokens: %w", err)
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal approved metadata: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO connect_tokens (
			connect_token_mac,
			redeem_secret_mac,
			tenant_id,
			environment,
			subject_id,
			approved_metadata,
			expires_at,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, connectTokenMAC, redeemSecretMAC, s.tenantID, s.environment, subjectID, metadataJSON, expiresAt, now)
	if err != nil {
		return fmt.Errorf("insert connect token: %w", err)
	}
	return nil
}

// RedeemConnectToken validates and activates a browser-submitted API key.
func (s *PostgresStore) RedeemConnectToken(ctx context.Context, input RedeemInput) (*Connection, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin redeem tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var (
		redeemSecretMAC []byte
		tenantID        string
		environment     string
		subjectID       string
		approvedJSON    []byte
		expiresAt       time.Time
		usedAt          *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT redeem_secret_mac, tenant_id, environment, subject_id, approved_metadata, expires_at, used_at
		FROM connect_tokens
		WHERE connect_token_mac = $1
		FOR UPDATE
	`, input.ConnectTokenMAC).Scan(&redeemSecretMAC, &tenantID, &environment, &subjectID, &approvedJSON, &expiresAt, &usedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidConnectToken
		}
		return nil, fmt.Errorf("lookup connect token: %w", err)
	}
	if usedAt != nil {
		return nil, ErrInvalidConnectToken
	}
	if connectTokenExpired(input.Now, expiresAt) {
		return nil, ErrExpiredConnectToken
	}
	if tenantID != input.TenantID || environment != input.Environment {
		return nil, ErrInvalidConnectToken
	}
	if !hmac.Equal(redeemSecretMAC, input.RedeemSecretMAC) {
		return nil, ErrInvalidConnectToken
	}

	var approved UserMetadata
	if err := json.Unmarshal(approvedJSON, &approved); err != nil {
		return nil, fmt.Errorf("decode approved metadata: %w", err)
	}
	if !equalMetadata(approved, input.Metadata) {
		return nil, ErrMetadataMismatch
	}

	code, validateErr := input.ValidateCredential(ctx, input.APIKey)
	if validateErr != nil {
		if code == "" {
			code = ValidationUpstreamUnreachable
		}
		return nil, &ValidationError{Code: code, Err: validateErr}
	}

	ciphertext, keyVersion, backend, err := input.Cipher.Encrypt(ctx, []byte(input.APIKey))
	if err != nil {
		return nil, fmt.Errorf("encrypt credential: %w", err)
	}

	var connection *Connection
	row := tx.QueryRow(ctx, `
		SELECT connection_id,
		       tenant_id,
		       environment,
		       subject_id,
		       rig_handle,
		       wastelands,
		       status,
		       credential_version,
		       record_version,
		       last_validated_at,
		       last_validation_error_code,
		       last_proxy_error_at,
		       created_at,
		       updated_at
		FROM connections
		WHERE tenant_id = $1 AND environment = $2 AND subject_id = $3
		FOR UPDATE
	`, input.TenantID, input.Environment, subjectID)

	connection, err = scanConnectionRow(row)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("scan existing connection: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		connection = nil
	}

	now := input.Now.UTC()
	if connection == nil {
		metadataJSON, err := json.Marshal(input.Metadata.Wastelands)
		if err != nil {
			return nil, fmt.Errorf("marshal wastelands: %w", err)
		}
		connectionID := ulid.Make().String()
		_, err = tx.Exec(ctx, `
			INSERT INTO connections (
				connection_id,
				tenant_id,
				environment,
				subject_id,
				rig_handle,
				wastelands,
				status,
				credential_ciphertext,
				credential_key_version,
				credential_encryption_backend,
				credential_version,
				record_version,
				last_validated_at,
				created_at,
				updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 1, 1, $11, $12, $12)
		`, connectionID, input.TenantID, input.Environment, subjectID, input.Metadata.RigHandle, metadataJSON, StatusActive, ciphertext, keyVersion, backend, now, now)
		if err != nil {
			return nil, fmt.Errorf("insert connection: %w", err)
		}
		connection, err = s.GetConnectionTx(ctx, tx, input.TenantID, input.Environment, subjectID, connectionID)
		if err != nil {
			return nil, err
		}
	} else {
		mergedMetadata := connection.Metadata.MergedWith(input.Metadata)
		metadataJSON, err := json.Marshal(mergedMetadata.Wastelands)
		if err != nil {
			return nil, fmt.Errorf("marshal merged wastelands: %w", err)
		}
		_, err = tx.Exec(ctx, `
			UPDATE connections
			SET rig_handle = $1,
			    wastelands = $2,
			    status = $3,
			    credential_ciphertext = $4,
			    credential_key_version = $5,
			    credential_encryption_backend = $6,
			    credential_version = credential_version + 1,
			    record_version = record_version + 1,
			    last_validated_at = $7,
			    last_validation_error_code = NULL,
			    updated_at = $7
			WHERE connection_id = $8 AND tenant_id = $9 AND environment = $10 AND subject_id = $11
		`, mergedMetadata.RigHandle, metadataJSON, StatusActive, ciphertext, keyVersion, backend, now, connection.ConnectionID, input.TenantID, input.Environment, subjectID)
		if err != nil {
			return nil, fmt.Errorf("update connection: %w", err)
		}
		connection, err = s.GetConnectionTx(ctx, tx, input.TenantID, input.Environment, subjectID, connection.ConnectionID)
		if err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE connect_tokens
		SET used_at = $2
		WHERE connect_token_mac = $1
	`, input.ConnectTokenMAC, now); err != nil {
		return nil, fmt.Errorf("mark connect token used: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (
			tenant_id, environment, subject_id, connection_id, actor_type, action, outcome, metadata
		) VALUES ($1, $2, $3, $4, 'browser-via-connect-token', 'connection_redeemed', 'success', '{}'::jsonb)
	`, input.TenantID, input.Environment, subjectID, connection.ConnectionID); err != nil {
		return nil, fmt.Errorf("insert audit log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit redeem tx: %w", err)
	}
	return connection, nil
}

// GetConnection fetches the stored non-secret connection view.
func (s *PostgresStore) GetConnection(
	ctx context.Context,
	tenantID, environment, subjectID, connectionID string,
) (*Connection, error) {
	return s.GetConnectionTx(ctx, s.pool, tenantID, environment, subjectID, connectionID)
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// GetConnectionTx fetches the stored non-secret connection view within a tx.
func (s *PostgresStore) GetConnectionTx(
	ctx context.Context,
	q queryRower,
	tenantID, environment, subjectID, connectionID string,
) (*Connection, error) {
	conn, err := scanConnectionRow(q.QueryRow(ctx, `
		SELECT connection_id,
		       tenant_id,
		       environment,
		       subject_id,
		       rig_handle,
		       wastelands,
		       status,
		       credential_version,
		       record_version,
		       last_validated_at,
		       last_validation_error_code,
		       last_proxy_error_at,
		       created_at,
		       updated_at
		FROM connections
		WHERE tenant_id = $1 AND environment = $2 AND subject_id = $3 AND connection_id = $4
	`, tenantID, environment, subjectID, connectionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get connection: %w", err)
	}
	return conn, nil
}

// GetConnectionCredential fetches and decrypts the stored DoltHub API key.
func (s *PostgresStore) GetConnectionCredential(
	ctx context.Context,
	tenantID, environment, subjectID, connectionID string,
	cipher CredentialCipher,
) (*Connection, string, error) {
	var (
		credentialCiphertext []byte
		keyVersion           string
		backend              string
	)
	row := s.pool.QueryRow(ctx, `
		SELECT connection_id,
		       tenant_id,
		       environment,
		       subject_id,
		       rig_handle,
		       wastelands,
		       status,
		       credential_version,
		       record_version,
		       last_validated_at,
		       last_validation_error_code,
		       last_proxy_error_at,
		       created_at,
		       updated_at,
		       credential_ciphertext,
		       credential_key_version,
		       credential_encryption_backend
		FROM connections
		WHERE tenant_id = $1 AND environment = $2 AND subject_id = $3 AND connection_id = $4
	`, tenantID, environment, subjectID, connectionID)

	var (
		connectionIDValue string
		tenantValue       string
		environmentValue  string
		subjectValue      string
		rigHandle         string
		wastelandsJSON    []byte
		status            string
		credentialVersion int
		recordVersion     int
		lastValidatedAt   *time.Time
		lastValidation    *string
		lastProxyErrorAt  *time.Time
		createdAt         time.Time
		updatedAt         time.Time
	)
	err := row.Scan(
		&connectionIDValue,
		&tenantValue,
		&environmentValue,
		&subjectValue,
		&rigHandle,
		&wastelandsJSON,
		&status,
		&credentialVersion,
		&recordVersion,
		&lastValidatedAt,
		&lastValidation,
		&lastProxyErrorAt,
		&createdAt,
		&updatedAt,
		&credentialCiphertext,
		&keyVersion,
		&backend,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("get credential: %w", err)
	}

	wastelands, err := decodeWastelands(wastelandsJSON)
	if err != nil {
		return nil, "", err
	}
	conn := &Connection{
		ConnectionID:            connectionIDValue,
		TenantID:                tenantValue,
		Environment:             environmentValue,
		SubjectID:               subjectValue,
		Metadata:                UserMetadata{RigHandle: rigHandle, Wastelands: wastelands},
		Status:                  ConnectionStatus(status),
		CredentialVersion:       credentialVersion,
		RecordVersion:           recordVersion,
		LastValidatedAt:         lastValidatedAt,
		CreatedAt:               createdAt,
		UpdatedAt:               updatedAt,
		LastProxyErrorAt:        lastProxyErrorAt,
		LastValidationErrorCode: validationCode(lastValidation),
	}
	plaintext, err := cipher.Decrypt(ctx, credentialCiphertext, keyVersion, backend)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt credential: %w", err)
	}
	return conn, string(plaintext), nil
}

// PatchRigHandle updates the stored rig handle for one connection.
func (s *PostgresStore) PatchRigHandle(
	ctx context.Context,
	tenantID, environment, subjectID, connectionID, rigHandle string,
	expectedRecordVersion int,
	now time.Time,
) (*Connection, error) {
	return s.patchMetadataTx(ctx, tenantID, environment, subjectID, connectionID, expectedRecordVersion, now, func(meta *UserMetadata) error {
		meta.RigHandle = rigHandle
		return nil
	})
}

// UpsertWasteland adds or replaces one Wasteland entry on a connection.
func (s *PostgresStore) UpsertWasteland(
	ctx context.Context,
	tenantID, environment, subjectID, connectionID string,
	wasteland WastelandConfig,
	expectedRecordVersion int,
	now time.Time,
) (*Connection, error) {
	return s.patchMetadataTx(ctx, tenantID, environment, subjectID, connectionID, expectedRecordVersion, now, func(meta *UserMetadata) error {
		meta.UpsertWasteland(wasteland)
		return nil
	})
}

// DeleteWasteland removes one Wasteland entry from a connection.
func (s *PostgresStore) DeleteWasteland(
	ctx context.Context,
	tenantID, environment, subjectID, connectionID, upstream string,
	expectedRecordVersion int,
	now time.Time,
) (*Connection, error) {
	return s.patchMetadataTx(ctx, tenantID, environment, subjectID, connectionID, expectedRecordVersion, now, func(meta *UserMetadata) error {
		if len(meta.Wastelands) <= 1 {
			return ErrLastWasteland
		}
		if !meta.RemoveWasteland(upstream) {
			return ErrWastelandNotFound
		}
		return nil
	})
}

// PatchWastelandSettings updates mutable fields on a Wasteland entry.
func (s *PostgresStore) PatchWastelandSettings(
	ctx context.Context,
	tenantID, environment, subjectID, connectionID, upstream, mode string,
	signing bool,
	expectedRecordVersion int,
	now time.Time,
) (*Connection, error) {
	return s.patchMetadataTx(ctx, tenantID, environment, subjectID, connectionID, expectedRecordVersion, now, func(meta *UserMetadata) error {
		entry := meta.FindWasteland(upstream)
		if entry == nil {
			return ErrWastelandNotFound
		}
		entry.Mode = mode
		entry.Signing = signing
		return nil
	})
}

func (s *PostgresStore) patchMetadataTx(
	ctx context.Context,
	tenantID, environment, subjectID, connectionID string,
	expectedRecordVersion int,
	now time.Time,
	mutate func(*UserMetadata) error,
) (*Connection, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin metadata tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var (
		rigHandle      string
		wastelandsJSON []byte
		recordVersion  int
	)
	err = tx.QueryRow(ctx, `
		SELECT rig_handle, wastelands, record_version
		FROM connections
		WHERE tenant_id = $1 AND environment = $2 AND subject_id = $3 AND connection_id = $4
		FOR UPDATE
	`, tenantID, environment, subjectID, connectionID).Scan(&rigHandle, &wastelandsJSON, &recordVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock connection: %w", err)
	}
	if recordVersion != expectedRecordVersion {
		return nil, ErrConflict
	}

	wastelands, err := decodeWastelands(wastelandsJSON)
	if err != nil {
		return nil, err
	}
	meta := &UserMetadata{RigHandle: rigHandle, Wastelands: wastelands}
	if err := mutate(meta); err != nil {
		return nil, err
	}
	if err := validateMetadata(*meta); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidMetadata, err)
	}

	encodedWastelands, err := json.Marshal(meta.Wastelands)
	if err != nil {
		return nil, fmt.Errorf("marshal wastelands: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE connections
		SET rig_handle = $1,
		    wastelands = $2,
		    record_version = record_version + 1,
		    updated_at = $3
		WHERE tenant_id = $4 AND environment = $5 AND subject_id = $6 AND connection_id = $7
	`, meta.RigHandle, encodedWastelands, now.UTC(), tenantID, environment, subjectID, connectionID); err != nil {
		return nil, fmt.Errorf("update metadata: %w", err)
	}

	conn, err := s.GetConnectionTx(ctx, tx, tenantID, environment, subjectID, connectionID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit metadata tx: %w", err)
	}
	return conn, nil
}

// UseServiceNonce records a nonce to prevent replayed service-auth requests.
func (s *PostgresStore) UseServiceNonce(ctx context.Context, keyID, nonce string, now, expiresAt time.Time) error {
	if err := s.reapExpiredServiceNonces(ctx, now.UTC()); err != nil {
		return fmt.Errorf("reap expired nonces: %w", err)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO service_request_nonces (key_id, nonce, expires_at)
		VALUES ($1, $2, $3)
	`, keyID, nonce, expiresAt)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return fmt.Errorf("insert nonce: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func connectTokenExpired(now, expiresAt time.Time) bool {
	return !now.Before(expiresAt)
}

func (s *PostgresStore) reapExpiredConnectTokens(ctx context.Context, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM connect_tokens
		WHERE expires_at <= $1
	`, now)
	return err
}

func (s *PostgresStore) reapExpiredServiceNonces(ctx context.Context, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM service_request_nonces
		WHERE expires_at <= $1
	`, now)
	return err
}

func scanConnectionRow(row pgx.Row) (*Connection, error) {
	var (
		connectionID     string
		tenantID         string
		environment      string
		subjectID        string
		rigHandle        string
		wastelandsJSON   []byte
		status           string
		credentialVer    int
		recordVersion    int
		lastValidatedAt  *time.Time
		lastValidation   *string
		lastProxyErrorAt *time.Time
		createdAt        time.Time
		updatedAt        time.Time
	)
	if err := row.Scan(
		&connectionID,
		&tenantID,
		&environment,
		&subjectID,
		&rigHandle,
		&wastelandsJSON,
		&status,
		&credentialVer,
		&recordVersion,
		&lastValidatedAt,
		&lastValidation,
		&lastProxyErrorAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}

	wastelands, err := decodeWastelands(wastelandsJSON)
	if err != nil {
		return nil, err
	}
	return &Connection{
		ConnectionID:            connectionID,
		TenantID:                tenantID,
		Environment:             environment,
		SubjectID:               subjectID,
		Metadata:                UserMetadata{RigHandle: rigHandle, Wastelands: wastelands},
		Status:                  ConnectionStatus(status),
		CredentialVersion:       credentialVer,
		RecordVersion:           recordVersion,
		LastValidatedAt:         lastValidatedAt,
		LastValidationErrorCode: validationCode(lastValidation),
		LastProxyErrorAt:        lastProxyErrorAt,
		CreatedAt:               createdAt,
		UpdatedAt:               updatedAt,
	}, nil
}

func decodeWastelands(raw []byte) ([]WastelandConfig, error) {
	var wastelands []WastelandConfig
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &wastelands); err != nil {
		return nil, fmt.Errorf("decode wastelands: %w", err)
	}
	return wastelands, nil
}

func validationCode(raw *string) ValidationErrorCode {
	if raw == nil || *raw == "" {
		return ""
	}
	return ValidationErrorCode(*raw)
}

func equalMetadata(a, b UserMetadata) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

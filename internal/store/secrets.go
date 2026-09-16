package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"os"
	"strings"
	"taskboard/internal/domain"
)

var ErrSecretKeyMissing = errors.New("secret backend key is not configured")

func secretKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("SHIPYARD_SECRET_KEY"))
	if raw == "" {
		return nil, ErrSecretKeyMissing
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:], nil
}
func sealSecret(value string) ([]byte, []byte, error) {
	key, err := secretKey()
	if err != nil {
		return nil, nil, err
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return g.Seal(nil, nonce, []byte(value), nil), nonce, nil
}
func openSecret(ciphertext, nonce []byte) (string, error) {
	key, err := secretKey()
	if err != nil {
		return "", err
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return "", err
	}
	plain, err := g.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("secret could not be decrypted")
	}
	return string(plain), nil
}

func (s *Store) CreateSecret(ctx context.Context, actor, name, description, envName, value string) (domain.Secret, error) {
	name, envName = strings.TrimSpace(name), strings.TrimSpace(envName)
	if name == "" || envName == "" || value == "" {
		return domain.Secret{}, errors.New("secret name, environment name and value are required")
	}
	if !isSafeSecretName(name) {
		return domain.Secret{}, errors.New("invalid secret name")
	}
	if !isSafeEnvName(envName) {
		return domain.Secret{}, errors.New("invalid secret environment name")
	}
	ciphertext, nonce, err := sealSecret(value)
	if err != nil {
		return domain.Secret{}, err
	}
	var out domain.Secret
	err = s.DB.QueryRow(ctx, `INSERT INTO secrets(name,description,env_name,ciphertext,nonce) VALUES($1,$2,$3,$4,$5) RETURNING id,name,description,env_name,false,created_at,updated_at`, name, strings.TrimSpace(description), envName, ciphertext, nonce).Scan(&out.ID, &out.Name, &out.Description, &out.EnvName, &out.Revoked, &out.CreatedAt, &out.UpdatedAt)
	if err == nil {
		err = s.RecordAudit(ctx, actor, "secret.created", "secret", out.ID, map[string]string{"name": out.Name, "env_name": out.EnvName})
	}
	return out, err
}
func isSafeSecretName(v string) bool {
	if len(v) == 0 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r == '_' || r == '-' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func isSafeEnvName(v string) bool {
	if len(v) == 0 || len(v) > 128 {
		return false
	}
	for i, r := range v {
		if !(r == '_' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func (s *Store) Secrets(ctx context.Context) ([]domain.Secret, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,name,description,env_name,revoked_at IS NOT NULL,created_at,updated_at FROM secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	secrets, err := pgx.CollectRows(rows, pgx.RowToStructByPos[domain.Secret])
	if err != nil {
		return nil, err
	}
	for i := range secrets {
		ids, e := s.secretAgents(ctx, secrets[i].ID)
		if e != nil {
			return nil, e
		}
		secrets[i].AgentIDs = ids
	}
	return secrets, nil
}
func (s *Store) secretAgents(ctx context.Context, secretID string) ([]string, error) {
	rows, err := s.DB.Query(ctx, "SELECT agent_id::text FROM secret_agents WHERE secret_id=$1 ORDER BY agent_id", secretID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
func (s *Store) SetSecretAgents(ctx context.Context, actor, secretID string, agentIDs []string) error {
	previous, err := s.secretAgents(ctx, secretID)
	if err != nil {
		return err
	}
	for _, id := range agentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		var conflict string
		err = s.DB.QueryRow(ctx, `SELECT s.name FROM secrets s JOIN secret_agents a ON a.secret_id=s.id WHERE a.agent_id=$1 AND s.env_name=(SELECT env_name FROM secrets WHERE id=$2) AND s.id<>$2 AND s.revoked_at IS NULL LIMIT 1`, id, secretID).Scan(&conflict)
		if err == nil {
			return errors.New("secret environment name is already assigned to this agent")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "DELETE FROM secret_agents WHERE secret_id=$1", secretID); err != nil {
		return err
	}
	for _, id := range agentIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, err = tx.Exec(ctx, "INSERT INTO secret_agents(secret_id,agent_id) VALUES($1,$2) ON CONFLICT DO NOTHING", secretID, id); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	old := map[string]bool{}
	for _, id := range previous {
		old[id] = true
	}
	next := map[string]bool{}
	for _, id := range agentIDs {
		next[id] = true
	}
	for id := range next {
		if !old[id] {
			if err := s.RecordAudit(ctx, actor, "secret.agent_assigned", "secret", secretID, map[string]string{"agent_id": id}); err != nil {
				return err
			}
		}
	}
	for id := range old {
		if !next[id] {
			if err := s.RecordAudit(ctx, actor, "secret.agent_unassigned", "secret", secretID, map[string]string{"agent_id": id}); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) ReplaceSecret(ctx context.Context, actor, secretID, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("secret value is required")
	}
	ciphertext, nonce, err := sealSecret(value)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, "UPDATE secrets SET ciphertext=$2,nonce=$3,revoked_at=NULL,updated_at=now() WHERE id=$1", secretID, ciphertext, nonce)
	if err == nil {
		err = s.RecordAudit(ctx, actor, "secret.replaced", "secret", secretID, nil)
	}
	return err
}
func (s *Store) DeleteSecret(ctx context.Context, actor, secretID string) error {
	if err := s.RecordAudit(ctx, actor, "secret.deleted", "secret", secretID, nil); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, "DELETE FROM secrets WHERE id=$1", secretID)
	return err
}
func (s *Store) RevokeSecret(ctx context.Context, actor, secretID string) error {
	_, err := s.DB.Exec(ctx, "UPDATE secrets SET revoked_at=now(),updated_at=now() WHERE id=$1", secretID)
	if err == nil {
		err = s.RecordAudit(ctx, actor, "secret.revoked", "secret", secretID, nil)
	}
	return err
}
func (s *Store) SecretValuesForAgent(ctx context.Context, agentID string) ([]domain.SecretValue, error) {
	rows, err := s.DB.Query(ctx, `SELECT s.id::text,s.env_name,s.ciphertext,s.nonce FROM secrets s JOIN secret_agents a ON a.secret_id=s.id WHERE a.agent_id=$1 AND s.revoked_at IS NULL`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SecretValue
	for rows.Next() {
		var id, env string
		var c, n []byte
		if err = rows.Scan(&id, &env, &c, &n); err != nil {
			return nil, err
		}
		v, e := openSecret(c, n)
		if e != nil {
			return nil, e
		}
		out = append(out, domain.SecretValue{ID: id, EnvName: env, Value: v})
	}
	return out, rows.Err()
}

func (s *Store) HasActiveSecretAssignment(ctx context.Context, envName string) (bool, error) {
	var exists bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM secrets s JOIN secret_agents a ON a.secret_id=s.id WHERE s.env_name=$1 AND s.revoked_at IS NULL)`, envName).Scan(&exists)
	return exists, err
}

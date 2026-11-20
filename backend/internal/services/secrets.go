package services

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
)

const (
	secretMaxValue = 65536
	// Redacted replaces secret values everywhere except GET /secrets/{name}/value.
	Redacted = "********"
)

var secretNameRe = regexp.MustCompile(`^[A-Za-z0-9/_+=.@-]{1,512}$`)

// SecretsService implements Secrets Manager-style secrets, encrypted at rest with AES-256-GCM.
type SecretsService struct {
	*Deps
	Repo repositories.SecretsRepo
	aead cipher.AEAD
}

// NewSecretsService derives the encryption key from env SECRETS_KEY, or else loads/creates a random
// key file (0600) in dataDir. It never logs key material.
func NewSecretsService(d *Deps, dataDir string) (*SecretsService, error) {
	key, err := loadSecretsKey(d, dataDir)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretsService{Deps: d, Repo: repositories.NewSecretsRepo(d.Store.DB), aead: aead}, nil
}

func loadSecretsKey(d *Deps, dataDir string) ([]byte, error) {
	if v := os.Getenv("SECRETS_KEY"); v != "" {
		k := sha256.Sum256([]byte("cloudsim-secrets-v1\x00" + v))
		return k[:], nil
	}
	path := filepath.Join(dataDir, "secrets.key")
	if b, err := os.ReadFile(path); err == nil {
		k, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("secrets key file %s is invalid (expected 64 hex chars)", path)
		}
		return k, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(hex.EncodeToString(k) + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	d.Log.Warn("SECRETS_KEY not set; generated a random secrets encryption key file — back it up with the database, or set SECRETS_KEY", "path", path)
	return k, nil
}

// seal encrypts plaintext bound (as additional data) to the secret and version ids.
func (s *SecretsService) seal(secretID, versionID, plaintext string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(plaintext), []byte(secretID+"|"+versionID)), nil
}

func (s *SecretsService) open(secretID, versionID string, ct []byte) (string, error) {
	ns := s.aead.NonceSize()
	if len(ct) < ns {
		return "", errors.New("ciphertext too short")
	}
	pt, err := s.aead.Open(nil, ct[:ns], ct[ns:], []byte(secretID+"|"+versionID))
	if err != nil {
		return "", errors.New("secret decryption failed (wrong SECRETS_KEY?)")
	}
	return string(pt), nil
}

func SecretARN(account, name string) string {
	return auth.ARN("secretsmanager", account, "secret:"+name)
}

type SecretVersionView struct {
	VersionID string   `json:"version_id"`
	Stages    []string `json:"version_stages"`
	CreatedAt string   `json:"created_at"`
}

// SecretView is secret metadata with the value redacted.
type SecretView struct {
	repositories.Secret
	ARN          string              `json:"arn"`
	SecretString string              `json:"secret_string"`
	Versions     []SecretVersionView `json:"versions,omitempty"`
}

func versionView(v repositories.SecretVersion) SecretVersionView {
	st := []string{}
	if v.Stage != "" {
		st = append(st, v.Stage)
	}
	return SecretVersionView{VersionID: v.VersionID, Stages: st, CreatedAt: v.CreatedAt}
}

func (s *SecretsService) view(ctx context.Context, sec *repositories.Secret, withVersions bool) (*SecretView, error) {
	v := &SecretView{Secret: *sec, ARN: SecretARN(sec.AccountID, sec.Name), SecretString: Redacted}
	if withVersions {
		vs, err := s.Repo.Versions(ctx, sec.SecretID)
		if err != nil {
			return nil, err
		}
		v.Versions = []SecretVersionView{}
		for _, x := range vs {
			v.Versions = append(v.Versions, versionView(x))
		}
	}
	return v, nil
}

type CreateSecretInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	SecretString string `json:"secret_string"`
}

func validateSecretValue(v string) error {
	if v == "" {
		return apierr.Validation("secret_string is required")
	}
	if len(v) > secretMaxValue {
		return apierr.Validation("secret_string must be at most %d bytes", secretMaxValue)
	}
	return nil
}

func (s *SecretsService) Create(ctx context.Context, account string, in CreateSecretInput) (*SecretView, error) {
	if !secretNameRe.MatchString(in.Name) {
		return nil, apierr.Validation("secret name must be 1-512 characters of letters, digits and /_+=.@-")
	}
	if len(in.Description) > 2048 {
		return nil, apierr.Validation("description must be at most 2048 characters")
	}
	if err := validateSecretValue(in.SecretString); err != nil {
		return nil, err
	}
	if _, err := s.Repo.Get(ctx, account, in.Name); err == nil {
		return nil, apierr.Conflict("secret %s already exists", in.Name)
	}
	now := db.Now()
	sec := &repositories.Secret{SecretID: ids.New("secret"), AccountID: account, Name: in.Name, Description: in.Description, CreatedAt: now, UpdatedAt: now}
	ver := &repositories.SecretVersion{VersionID: newVersionID(), CreatedAt: now}
	ct, err := s.seal(sec.SecretID, ver.VersionID, in.SecretString)
	if err != nil {
		return nil, err
	}
	ver.Ciphertext = ct
	if err := s.Repo.Create(ctx, sec, ver); err != nil {
		return nil, RepoErr(err, "secret "+in.Name)
	}
	sec.CurrentVersionID = ver.VersionID
	s.RecordEvent(ctx, account, sec.SecretID, "secret", "created", "Secret "+sec.Name+" created", nil)
	return s.view(ctx, sec, true)
}

func newVersionID() string {
	t := ids.Token()
	return t[:8] + "-" + t[8:12] + "-" + t[12:16] + "-" + t[16:20] + "-" + t[20:32]
}

func (s *SecretsService) get(ctx context.Context, account, name string) (*repositories.Secret, error) {
	sec, err := s.Repo.Get(ctx, account, name)
	if err != nil {
		return nil, RepoErr(err, "secret "+name)
	}
	return sec, nil
}

func (s *SecretsService) Describe(ctx context.Context, account, name string) (*SecretView, error) {
	sec, err := s.get(ctx, account, name)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, sec, true)
}

func (s *SecretsService) List(ctx context.Context, account string) ([]SecretView, error) {
	l, err := s.Repo.List(ctx, account)
	if err != nil {
		return nil, err
	}
	out := make([]SecretView, 0, len(l))
	for i := range l {
		v, _ := s.view(ctx, &l[i], false)
		out = append(out, *v)
	}
	return out, nil
}

type SecretValue struct {
	Name          string   `json:"name"`
	ARN           string   `json:"arn"`
	VersionID     string   `json:"version_id"`
	VersionStages []string `json:"version_stages"`
	SecretString  string   `json:"secret_string"`
	CreatedAt     string   `json:"created_at"`
}

// GetValue decrypts the current (or a specific / AWSPREVIOUS) version. Callers must audit the access.
func (s *SecretsService) GetValue(ctx context.Context, account, name, versionID, stage string) (*SecretValue, error) {
	sec, err := s.get(ctx, account, name)
	if err != nil {
		return nil, err
	}
	switch {
	case versionID != "":
	case stage == "" || stage == "AWSCURRENT":
		versionID = sec.CurrentVersionID
	case stage == "AWSPREVIOUS":
		vs, err := s.Repo.Versions(ctx, sec.SecretID)
		if err != nil {
			return nil, err
		}
		for _, v := range vs {
			if v.Stage == "AWSPREVIOUS" {
				versionID = v.VersionID
			}
		}
		if versionID == "" {
			return nil, apierr.NotFound("secret %s has no AWSPREVIOUS version", name)
		}
	default:
		return nil, apierr.Validation("version_stage must be AWSCURRENT or AWSPREVIOUS")
	}
	v, err := s.Repo.GetVersion(ctx, sec.SecretID, versionID)
	if err != nil {
		return nil, RepoErr(err, "secret version "+versionID)
	}
	pt, err := s.open(sec.SecretID, v.VersionID, v.Ciphertext)
	if err != nil {
		return nil, err
	}
	_ = s.Repo.Touch(ctx, sec.SecretID, db.Now())
	s.RecordEvent(ctx, account, sec.SecretID, "secret", "value_retrieved", "Value of secret "+name+" retrieved (version "+v.VersionID+")", nil)
	return &SecretValue{Name: sec.Name, ARN: SecretARN(account, sec.Name), VersionID: v.VersionID,
		VersionStages: versionView(*v).Stages, SecretString: pt, CreatedAt: v.CreatedAt}, nil
}

type UpdateSecretInput struct {
	SecretString *string `json:"secret_string"`
	Description  *string `json:"description"`
}

func (s *SecretsService) Update(ctx context.Context, account, name string, in UpdateSecretInput) (*SecretView, error) {
	sec, err := s.get(ctx, account, name)
	if err != nil {
		return nil, err
	}
	if in.SecretString == nil && in.Description == nil {
		return nil, apierr.Validation("provide secret_string and/or description")
	}
	now := db.Now()
	if in.Description != nil {
		if len(*in.Description) > 2048 {
			return nil, apierr.Validation("description must be at most 2048 characters")
		}
		if err := s.Repo.UpdateDescription(ctx, sec.SecretID, *in.Description, now); err != nil {
			return nil, err
		}
	}
	if in.SecretString != nil {
		if err := validateSecretValue(*in.SecretString); err != nil {
			return nil, err
		}
		ver := &repositories.SecretVersion{VersionID: newVersionID(), CreatedAt: now}
		if ver.Ciphertext, err = s.seal(sec.SecretID, ver.VersionID, *in.SecretString); err != nil {
			return nil, err
		}
		if err := s.Repo.PutVersion(ctx, sec.SecretID, ver); err != nil {
			return nil, err
		}
		s.RecordEvent(ctx, account, sec.SecretID, "secret", "version_created", "New version "+ver.VersionID+" of secret "+name, nil)
	}
	return s.Describe(ctx, account, name)
}

func (s *SecretsService) Delete(ctx context.Context, account, name string) error {
	sec, err := s.get(ctx, account, name)
	if err != nil {
		return err
	}
	if err := s.Repo.Delete(ctx, sec.SecretID); err != nil {
		return RepoErr(err, "secret "+name)
	}
	s.RecordEvent(ctx, account, sec.SecretID, "secret", "deleted", "Secret "+name+" deleted (no recovery window in CloudSim)", nil)
	return nil
}

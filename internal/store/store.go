// Package store persists the feed and state: in Cloudflare R2, or in a local
// directory for dry runs.
package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Store reads and writes whole objects by key.
type Store interface {
	// Get returns the object, or found=false if it doesn't exist.
	Get(ctx context.Context, key string) (body []byte, found bool, err error)
	Put(ctx context.Context, key string, body []byte, contentType, cacheControl string) error
}

// Local stores objects as files under Dir.
type Local struct{ Dir string }

func (l Local) path(key string) string { return filepath.Join(l.Dir, filepath.FromSlash(key)) }

func (l Local) Get(_ context.Context, key string) ([]byte, bool, error) {
	b, err := os.ReadFile(l.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// Put writes atomically via a temporary file and rename.
func (l Local) Put(_ context.Context, key string, body []byte, _, _ string) error {
	p := l.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Path returns the local file path of a key (for the dry-run summary).
func (l Local) Path(key string) string { return l.path(key) }

// R2 stores objects in a Cloudflare R2 bucket through its S3-compatible API.
type R2 struct {
	client *s3.Client
	bucket string
}

// R2Config holds the connection settings.
type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	// Endpoint overrides https://<AccountID>.r2.cloudflarestorage.com (tests).
	Endpoint   string
	HTTPClient *http.Client
}

// NewR2 returns an R2 store.
func NewR2(cfg R2Config) *R2 {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	client := s3.New(s3.Options{
		BaseEndpoint:     aws.String(endpoint),
		Region:           "auto",
		Credentials:      credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		UsePathStyle:     true,
		HTTPClient:       hc,
		RetryMaxAttempts: 3,
		// R2 doesn't support every default flexible checksum the SDK sends.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &R2{client: client, bucket: cfg.Bucket}
}

func (r *R2) Get(ctx context.Context, key string) ([]byte, bool, error) {
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key)})
	if err != nil {
		var nsk *types.NoSuchKey
		var re *smithyhttp.ResponseError
		if errors.As(err, &nsk) || errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("r2 get %s: %w", key, err)
	}
	defer out.Body.Close()
	b, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, false, fmt.Errorf("r2 get %s: %w", key, err)
	}
	return b, true, nil
}

func (r *R2) Put(ctx context.Context, key string, body []byte, contentType, cacheControl string) error {
	in := &s3.PutObjectInput{
		Bucket:        aws.String(r.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
		ContentType:   aws.String(contentType),
	}
	if cacheControl != "" {
		in.CacheControl = aws.String(cacheControl)
	}
	if _, err := r.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("r2 put %s: %w", key, err)
	}
	return nil
}

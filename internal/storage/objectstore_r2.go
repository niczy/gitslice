package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type R2ObjectStoreConfig struct {
	Bucket          string
	Prefix          string
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
}

type R2ObjectStore struct {
	client *s3.Client
	bucket string
	prefix string
}

func NewR2ObjectStore(cfg R2ObjectStoreConfig) (*R2ObjectStore, error) {
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("R2 bucket is required")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("R2 endpoint is required")
	}
	accessKeyID := strings.TrimSpace(cfg.AccessKeyID)
	if accessKeyID == "" {
		return nil, fmt.Errorf("R2 access key id is required")
	}
	secretAccessKey := strings.TrimSpace(cfg.SecretAccessKey)
	if secretAccessKey == "" {
		return nil, fmt.Errorf("R2 secret access key is required")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "auto"
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(
		context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = cfg.UsePathStyle
	})

	return &R2ObjectStore{
		client: client,
		bucket: bucket,
		prefix: normalizeObjectStorePrefix(cfg.Prefix),
	}, nil
}

func normalizeObjectStorePrefix(prefix string) string {
	trimmed := strings.Trim(strings.TrimSpace(prefix), "/")
	if trimmed == "" {
		return ""
	}
	return trimmed + "/"
}

func (s *R2ObjectStore) objectKey(key string) string {
	return s.prefix + strings.TrimLeft(strings.TrimSpace(key), "/")
}

func (s *R2ObjectStore) PutObject(ctx context.Context, key string, body []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
		Body:   bytes.NewReader(body),
	})
	return err
}

func (s *R2ObjectStore) GetObject(ctx context.Context, key string) ([]byte, error) {
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		if isS3ObjectNotFound(err) {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	defer output.Body.Close()

	body, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (s *R2ObjectStore) HasObject(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err == nil {
		return true, nil
	}
	if isS3ObjectNotFound(err) {
		return false, nil
	}
	return false, err
}

func (s *R2ObjectStore) DeleteObject(ctx context.Context, key string) error {
	exists, err := s.HasObject(ctx, key)
	if err != nil {
		return err
	}
	if !exists {
		return ErrEntryNotFound
	}

	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	return err
}

func isS3ObjectNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		if apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound" {
			return true
		}
	}

	var responseErr interface{ HTTPStatusCode() int }
	if errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == http.StatusNotFound {
		return true
	}

	return false
}

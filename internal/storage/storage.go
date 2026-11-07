package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

var (
	ErrNotFound = errors.New("object not found")
	ErrTooLarge = errors.New("object too large")
)

type Options struct {
	STSEndpoint        string
	STSRoleARN         string
	STSRoleSessionName string
	STSDurationSeconds int32
	S3Endpoint         string
	S3Region           string
	S3PathStyleAccess  bool
}

// Client は検証済みの WebIdentityToken ごとに STS で一時クレデンシャルを取り、
// それで S3 を操作する。長期クレデンシャルは持たない。
type Client struct {
	opts Options
	sts  *sts.Client
}

func NewClient(opts Options) *Client {
	stsClient := sts.New(sts.Options{
		Region:       opts.S3Region,
		BaseEndpoint: aws.String(opts.STSEndpoint),
		Credentials:  aws.AnonymousCredentials{},
	})
	return &Client{opts: opts, sts: stsClient}
}

type Session struct {
	s3 *s3.Client
}

func (c *Client) AssumeWithWebIdentity(ctx context.Context, token string) (*Session, error) {
	out, err := c.sts.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(c.opts.STSRoleARN),
		RoleSessionName:  aws.String(c.opts.STSRoleSessionName),
		WebIdentityToken: aws.String(token),
		DurationSeconds:  aws.Int32(c.opts.STSDurationSeconds),
	})
	if err != nil {
		return nil, fmt.Errorf("assume role: %w", err)
	}
	cred := out.Credentials
	provider := credentials.NewStaticCredentialsProvider(
		aws.ToString(cred.AccessKeyId),
		aws.ToString(cred.SecretAccessKey),
		aws.ToString(cred.SessionToken),
	)
	s3Client := s3.New(s3.Options{
		Region:       c.opts.S3Region,
		BaseEndpoint: aws.String(c.opts.S3Endpoint),
		Credentials:  provider,
		UsePathStyle: c.opts.S3PathStyleAccess,
	})
	return &Session{s3: s3Client}, nil
}

func (s *Session) Get(ctx context.Context, bucket, key string, maxBytes int64) ([]byte, error) {
	out, err := s.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noSuchKey *s3types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer out.Body.Close()

	if out.ContentLength != nil && *out.ContentLength > maxBytes {
		return nil, ErrTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(out.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, ErrTooLarge
	}
	return body, nil
}

func (s *Session) Put(ctx context.Context, bucket, key, contentType string, body []byte) error {
	_, err := s.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// S3Config is what NewS3 needs. Region defaults to us-east-1.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	PathStyle       bool
}

// S3 is a Storage on any S3-compatible service.
type S3 struct {
	client *s3.Client
	bucket string
}

var _ Storage = (*S3)(nil)

// NewS3 builds a client. It does not contact the server.
func NewS3(cfg S3Config) (*S3, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("bucket is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("access key and secret key are required")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	awsCfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.PathStyle
		// Several S3-compatible providers reject the newer default checksums.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &S3{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3) put(ctx context.Context, in *s3.PutObjectInput, body []byte) (Object, error) {
	in.Bucket = aws.String(s.bucket)
	in.Body = bytes.NewReader(body)
	in.ContentLength = aws.Int64(int64(len(body)))
	out, err := s.client.PutObject(ctx, in)
	if err != nil {
		return Object{}, mapErr(err)
	}
	return Object{Key: aws.ToString(in.Key), Size: int64(len(body)), ETag: aws.ToString(out.ETag), Metadata: lowerKeys(in.Metadata)}, nil
}

func (s *S3) Put(ctx context.Context, key string, body []byte, meta map[string]string) (Object, error) {
	return s.put(ctx, &s3.PutObjectInput{Key: aws.String(key), Metadata: meta}, body)
}

func (s *S3) PutIfAbsent(ctx context.Context, key string, body []byte) (Object, error) {
	return s.put(ctx, &s3.PutObjectInput{Key: aws.String(key), IfNoneMatch: aws.String("*")}, body)
}

func (s *S3) PutIfMatch(ctx context.Context, key string, body []byte, etag string) (Object, error) {
	return s.put(ctx, &s3.PutObjectInput{Key: aws.String(key), IfMatch: aws.String(etag)}, body)
}

func (s *S3) Get(ctx context.Context, key string) ([]byte, Object, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, Object{}, mapErr(err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, Object{}, err
	}
	return data, Object{
		Key: key, Size: int64(len(data)), ETag: aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified), Metadata: lowerKeys(out.Metadata),
	}, nil
}

func (s *S3) Head(ctx context.Context, key string) (Object, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return Object{}, mapErr(err)
	}
	return Object{
		Key: key, Size: aws.ToInt64(out.ContentLength), ETag: aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified), Metadata: lowerKeys(out.Metadata),
	}, nil
}

func (s *S3) List(ctx context.Context, prefix string) ([]Object, error) {
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)})
	var out []Object
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, mapErr(err)
		}
		for _, o := range page.Contents {
			out = append(out, Object{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size), ETag: aws.ToString(o.ETag), LastModified: aws.ToTime(o.LastModified)})
		}
	}
	return out, nil
}

func (s *S3) ListPrefixes(ctx context.Context, prefix string) ([]string, error) {
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix), Delimiter: aws.String("/")})
	var out []string
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, mapErr(err)
		}
		for _, cp := range page.CommonPrefixes {
			out = append(out, aws.ToString(cp.Prefix))
		}
	}
	return out, nil
}

func (s *S3) Copy(ctx context.Context, srcKey, dstKey string) (Object, error) {
	out, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(s.bucket),
		Key:        aws.String(dstKey),
		CopySource: aws.String(s.bucket + "/" + escapeKey(srcKey)),
	})
	if err != nil {
		return Object{}, mapErr(err)
	}
	obj := Object{Key: dstKey}
	if out.CopyObjectResult != nil {
		obj.ETag = aws.ToString(out.CopyObjectResult.ETag)
	}
	return obj, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return mapErr(err)
}

// EnsureBucket creates the bucket if it does not exist. Used by tests.
func (s *S3) EnsureBucket(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); err == nil {
		return nil
	}
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "BucketAlreadyOwnedByYou" || apiErr.ErrorCode() == "BucketAlreadyExists") {
		return nil
	}
	return mapErr(err)
}

// escapeKey URL-encodes each path segment for the CopySource header.
func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(url.QueryEscape(p), "+", "%20")
	}
	return strings.Join(parts, "/")
}

func lowerKeys(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(k)] = v
	}
	return out
}

// mapErr turns SDK errors into the package's sentinel errors.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return fmt.Errorf("%w: %v", ErrNotFound, err)
		case "PreconditionFailed", "ConditionalRequestConflict":
			return fmt.Errorf("%w: %v", ErrPreconditionFailed, err)
		case "AccessDenied", "Forbidden", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return fmt.Errorf("%w: %v", ErrAccessDenied, err)
		case "NotImplemented":
			return fmt.Errorf("%w: %v", ErrConditionalUnsupported, err)
		}
	}
	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.HTTPStatusCode() {
		case 404:
			return fmt.Errorf("%w: %v", ErrNotFound, err)
		case 412:
			return fmt.Errorf("%w: %v", ErrPreconditionFailed, err)
		case 403:
			return fmt.Errorf("%w: %v", ErrAccessDenied, err)
		case 501:
			return fmt.Errorf("%w: %v", ErrConditionalUnsupported, err)
		}
	}
	return err
}

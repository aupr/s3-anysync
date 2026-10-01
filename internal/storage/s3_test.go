package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
)

func TestMapErr(t *testing.T) {
	cases := map[string]error{
		"NoSuchKey":                  ErrNotFound,
		"NotFound":                   ErrNotFound,
		"PreconditionFailed":         ErrPreconditionFailed,
		"ConditionalRequestConflict": ErrPreconditionFailed,
		"AccessDenied":               ErrAccessDenied,
		"InvalidAccessKeyId":         ErrAccessDenied,
		"SignatureDoesNotMatch":      ErrAccessDenied,
		"NotImplemented":             ErrConditionalUnsupported,
	}
	for code, want := range cases {
		err := mapErr(&smithy.GenericAPIError{Code: code, Message: "x"})
		if !errors.Is(err, want) {
			t.Errorf("mapErr(%s) = %v, want %v", code, err, want)
		}
	}
	other := errors.New("boom")
	if mapErr(other) != other {
		t.Error("unknown errors must pass through unchanged")
	}
	if mapErr(nil) != nil {
		t.Error("nil must stay nil")
	}
}

func TestMapErrKeepsAPIError(t *testing.T) {
	err := mapErr(&smithy.GenericAPIError{Code: "AccessDenied", Message: "x"})
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "AccessDenied" {
		t.Fatalf("APIError lost: %v", err)
	}
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("sentinel lost: %v", err)
	}
}

func TestMapErrNoSuchBucket(t *testing.T) {
	err := mapErr(&smithy.GenericAPIError{Code: "NoSuchBucket", Message: "x"})
	if !errors.Is(err, ErrNoSuchBucket) || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPutRejectsBadMetadata(t *testing.T) {
	s, err := NewS3(S3Config{Endpoint: "http://127.0.0.1:1", Bucket: "b", AccessKeyID: "a", SecretAccessKey: "s", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"café", "a\nb"} {
		_, err := s.Put(context.Background(), "k", nil, map[string]string{"x": v})
		if err == nil || !strings.Contains(err.Error(), "non-ASCII or control") {
			t.Errorf("value %q: err = %v", v, err)
		}
	}
	if _, err := s.Put(context.Background(), "k", nil, map[string]string{"bad\x00key": "v"}); err == nil {
		t.Error("bad key accepted")
	}
}

func TestEscapeKey(t *testing.T) {
	if got := escapeKey("dir a/c+d/é.json"); got != "dir%20a/c%2Bd/%C3%A9.json" {
		t.Fatalf("escapeKey = %q", got)
	}
}

func TestNewS3Validates(t *testing.T) {
	if _, err := NewS3(S3Config{AccessKeyID: "k", SecretAccessKey: "s"}); err == nil {
		t.Fatal("missing bucket accepted")
	}
	if _, err := NewS3(S3Config{Bucket: "b"}); err == nil {
		t.Fatal("missing credentials accepted")
	}
}

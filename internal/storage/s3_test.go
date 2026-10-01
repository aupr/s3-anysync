package storage

import (
	"errors"
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

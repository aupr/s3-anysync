package storage_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/niceit/s3-anysync/internal/storage"
)

// runContract checks behaviour every Storage implementation must share.
func runContract(t *testing.T, s storage.Storage, prefix string) {
	ctx := context.Background()
	key := func(name string) string { return storage.JoinKey(prefix, name) }

	t.Run("put get head", func(t *testing.T) {
		put, err := s.Put(ctx, key("a/file.txt"), []byte("hello"), map[string]string{"sha256": "abc"})
		if err != nil {
			t.Fatal(err)
		}
		if put.ETag == "" {
			t.Fatal("put returned no etag")
		}
		body, obj, err := s.Get(ctx, key("a/file.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "hello" || obj.Metadata["sha256"] != "abc" {
			t.Fatalf("get = %q %v", body, obj.Metadata)
		}
		head, err := s.Head(ctx, key("a/file.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if head.Size != 5 || head.Metadata["sha256"] != "abc" {
			t.Fatalf("head = %+v", head)
		}
	})

	t.Run("missing", func(t *testing.T) {
		if _, _, err := s.Get(ctx, key("missing")); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("get missing: %v", err)
		}
		if _, err := s.Head(ctx, key("missing")); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("head missing: %v", err)
		}
	})

	t.Run("list", func(t *testing.T) {
		for _, n := range []string{"l/1", "l/2/x", "l/3"} {
			if _, err := s.Put(ctx, key(n), []byte(n), nil); err != nil {
				t.Fatal(err)
			}
		}
		objs, err := s.List(ctx, key("l")+"/")
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, o := range objs {
			keys = append(keys, o.Key)
		}
		if want := []string{key("l/1"), key("l/2/x"), key("l/3")}; !reflect.DeepEqual(keys, want) {
			t.Fatalf("list = %v, want %v", keys, want)
		}
		prefixes, err := s.ListPrefixes(ctx, key("l")+"/")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{key("l/2") + "/"}; !reflect.DeepEqual(prefixes, want) {
			t.Fatalf("prefixes = %v, want %v", prefixes, want)
		}
		// ListPrefixes on a prefix with no children returns empty
		noPrefixes, err := s.ListPrefixes(ctx, key("no-such-dir")+"/")
		if err != nil {
			t.Fatal(err)
		}
		if len(noPrefixes) != 0 {
			t.Fatalf("ListPrefixes on non-existent prefix = %v, want empty", noPrefixes)
		}
	})

	t.Run("copy keeps metadata", func(t *testing.T) {
		if _, err := s.Put(ctx, key("c/src"), []byte("data"), map[string]string{"sha256": "h"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Copy(ctx, key("c/src"), key("c/dst copy+1")); err != nil {
			t.Fatal(err)
		}
		body, obj, err := s.Get(ctx, key("c/dst copy+1"))
		if err != nil || string(body) != "data" || obj.Metadata["sha256"] != "h" {
			t.Fatalf("copied = %q %v %v", body, obj.Metadata, err)
		}
		if _, err := s.Copy(ctx, key("c/none"), key("c/x")); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("copy missing: %v", err)
		}
	})

	t.Run("metadata keys are lowercased", func(t *testing.T) {
		if _, err := s.Put(ctx, key("m/case"), []byte("data"), map[string]string{"SHA256": "abc", "Machine": "m1"}); err != nil {
			t.Fatal(err)
		}
		_, obj, err := s.Get(ctx, key("m/case"))
		if err != nil {
			t.Fatal(err)
		}
		if obj.Metadata["sha256"] != "abc" || obj.Metadata["machine"] != "m1" {
			t.Fatalf("metadata keys not lowercased: %v", obj.Metadata)
		}
		head, err := s.Head(ctx, key("m/case"))
		if err != nil {
			t.Fatal(err)
		}
		if head.Metadata["sha256"] != "abc" || head.Metadata["machine"] != "m1" {
			t.Fatalf("head metadata keys not lowercased: %v", head.Metadata)
		}
	})

	t.Run("overwrite replaces", func(t *testing.T) {
		if _, err := s.Put(ctx, key("ow"), []byte("one"), map[string]string{"sha256": "1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put(ctx, key("ow"), []byte("two"), map[string]string{"sha256": "2"}); err != nil {
			t.Fatal(err)
		}
		body, obj, err := s.Get(ctx, key("ow"))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "two" || obj.Metadata["sha256"] != "2" {
			t.Fatalf("overwrite: got body=%q sha256=%q, want body=two sha256=2", body, obj.Metadata["sha256"])
		}
	})

	t.Run("zero-byte body", func(t *testing.T) {
		if _, err := s.Put(ctx, key("empty"), []byte{}, nil); err != nil {
			t.Fatal(err)
		}
		body, _, err := s.Get(ctx, key("empty"))
		if err != nil {
			t.Fatal(err)
		}
		if len(body) != 0 {
			t.Fatalf("get empty body: got len %d, want 0", len(body))
		}
		head, err := s.Head(ctx, key("empty"))
		if err != nil {
			t.Fatal(err)
		}
		if head.Size != 0 {
			t.Fatalf("head empty size: got %d, want 0", head.Size)
		}
	})

	t.Run("delete", func(t *testing.T) {
		if _, err := s.Put(ctx, key("d/x"), []byte("x"), nil); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, key("d/x")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Get(ctx, key("d/x")); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("after delete: %v", err)
		}
		if err := s.Delete(ctx, key("d/x")); err != nil {
			t.Fatalf("delete missing: %v", err)
		}
	})

	t.Run("conditional writes", func(t *testing.T) {
		k := key("cond")
		first, err := s.PutIfAbsent(ctx, k, []byte("1"))
		if errors.Is(err, storage.ErrConditionalUnsupported) {
			t.Skip("conditional writes not supported")
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutIfAbsent(ctx, k, []byte("2")); !errors.Is(err, storage.ErrPreconditionFailed) {
			t.Fatalf("second PutIfAbsent: %v", err)
		}
		second, err := s.PutIfMatch(ctx, k, []byte("3"), first.ETag)
		if err != nil {
			t.Fatalf("PutIfMatch with current etag: %v", err)
		}
		// ETag changes on rewrite
		if second.ETag == "" || second.ETag == first.ETag {
			t.Fatalf("ETag did not change: first=%q second=%q", first.ETag, second.ETag)
		}
		// PutIfMatch with new ETag succeeds
		if _, err := s.PutIfMatch(ctx, k, []byte("5"), second.ETag); err != nil {
			t.Fatalf("PutIfMatch with new etag: %v", err)
		}
		if _, err := s.PutIfMatch(ctx, k, []byte("4"), first.ETag); !errors.Is(err, storage.ErrPreconditionFailed) {
			t.Fatalf("PutIfMatch with stale etag: %v", err)
		}
		if body, _, _ := s.Get(ctx, k); string(body) != "5" {
			t.Fatalf("body = %q", body)
		}
		// PutIfMatch on missing key should return error
		if _, err := s.PutIfMatch(ctx, key("cond-missing"), []byte("x"), "\"abc\""); err == nil {
			t.Fatal("PutIfMatch on missing key should return error")
		} else if !errors.Is(err, storage.ErrPreconditionFailed) && !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("PutIfMatch on missing key: got %v, want ErrPreconditionFailed or ErrNotFound", err)
		}
	})
}

func TestMemoryContract(t *testing.T) {
	runContract(t, storage.NewMemory(), "contract")
}

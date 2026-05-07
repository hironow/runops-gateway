package phonewave

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestSingleArchiveRouter_AlwaysReturnsEmpty(t *testing.T) {
	r := NewSingleArchiveRouter()
	for _, p := range []string{
		"/abs/foo/file.md",
		"/var/lib/anything",
		"",
	} {
		got, err := r.ResolveProjectID(context.Background(), p)
		if err != nil {
			t.Fatalf("path=%q unexpected err: %v", p, err)
		}
		if got != "" {
			t.Errorf("path=%q project=%q want empty", p, got)
		}
	}
	if r.Mode() != "single" {
		t.Errorf("Mode=%q want single", r.Mode())
	}
}

func TestMultiArchiveRouter_RoutesByPrefix(t *testing.T) {
	r, err := NewMultiArchiveRouter(map[string]string{
		"foo": "/work/foo",
		"bar": "/work/bar",
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if r.Mode() != "multi" {
		t.Errorf("Mode=%q want multi", r.Mode())
	}

	cases := []struct {
		name      string
		path      string
		wantPID   string
		wantError bool
	}{
		{"foo nested file", "/work/foo/2026-05/abc.md", "foo", false},
		{"bar nested file", "/work/bar/x.md", "bar", false},
		{"exact dir match", "/work/foo", "foo", false},
		{"unmapped sibling", "/work/baz/x.md", "", true},
		{"prefix-but-not-boundary (foo vs foobar)", "/work/foobar/x.md", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.ResolveProjectID(context.Background(), tc.path)
			if tc.wantError {
				if !errors.Is(err, ErrPathNotMapped) {
					t.Fatalf("want ErrPathNotMapped, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.wantPID {
				t.Errorf("got=%q want=%q", got, tc.wantPID)
			}
		})
	}
}

func TestMultiArchiveRouter_RejectsDuplicateDir(t *testing.T) {
	_, err := NewMultiArchiveRouter(map[string]string{
		"foo": "/work/shared",
		"bar": "/work/shared",
	})
	if err == nil {
		t.Fatalf("duplicate cleaned dir should be rejected")
	}
}

func TestMultiArchiveRouter_RejectsNestedDirs(t *testing.T) {
	cases := []map[string]string{
		// inner is nested under outer
		{"outer": "/work/a", "inner": "/work/a/sub"},
		// reverse declaration order
		{"inner": "/work/a/sub", "outer": "/work/a"},
		// non-trivial nesting depth
		{"outer": "/work", "deep": "/work/a/b/c"},
	}
	for i, m := range cases {
		_, err := NewMultiArchiveRouter(m)
		if err == nil {
			t.Errorf("case %d: nested dirs %v should be rejected", i, m)
		}
	}
}

func TestMultiArchiveRouter_DirtyPathsAreNormalized(t *testing.T) {
	// Whether the operator passes "/work/foo/" or "/work/foo" should
	// not change the routing. filepath.Clean handles trailing slashes.
	r, err := NewMultiArchiveRouter(map[string]string{
		"foo": "/work/foo/",
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	got, err := r.ResolveProjectID(context.Background(),
		filepath.Join("/work/foo", "2026-05", "abc.md"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "foo" {
		t.Errorf("got=%q want foo", got)
	}
}

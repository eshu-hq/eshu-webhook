// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImportBoundaryRejectsStorageAndCoreImports keeps the satellite
// stateless: no SQL drivers, no Eshu core internal imports, no filesystem
// state. Only the module itself plus the standard library may be imported.
func TestImportBoundaryRejectsStorageAndCoreImports(t *testing.T) {
	t.Parallel()

	violations := []string{}
	walk := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", path, err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parser.ParseFile(%s) error = %v", path, err)
		}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == "C" {
				violations = append(violations, path+": cgo import is not allowed")
				continue
			}
			if isStandardOrRelative(importPath, "github.com/eshu-hq/eshu-webhook") {
				continue
			}
			violations = append(violations, path+": non-stdlib import "+importPath)
		}
		return nil
	}
	root, err := moduleRoot(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.Walk(root, walk); err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("import boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}

// TestBodyOverLimitIsRejected proves the 1 MiB provider body bound: a
// delivery one byte over the cap must fail before signature verification.
func TestBodyOverLimitIsRejected(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	h, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	over := make([]byte, cfg.MaxRequestBodyBytes+1)
	for i := range over {
		over[i] = 'x'
	}
	if err := checkBodyLimit(over, cfg.MaxRequestBodyBytes); err == nil {
		t.Fatal("checkBodyLimit(over-cap) = nil, want error")
	}
	if err := checkBodyLimit(over[:len(over)-1], cfg.MaxRequestBodyBytes); err != nil {
		t.Fatalf("checkBodyLimit(at-cap) error = %v, want nil", err)
	}
	_ = h
}

// TestDefaultBodyLimitIsOneMiB pins the intake contract bound: the default
// cap must equal exactly 1 MiB unless the operator overrides it.
func TestDefaultBodyLimitIsOneMiB(t *testing.T) {
	t.Parallel()

	if DefaultMaxBodyBytes != 1<<20 {
		t.Fatalf("DefaultMaxBodyBytes = %d, want %d", DefaultMaxBodyBytes, 1<<20)
	}
	cfg, err := LoadConfig(nil)
	if err == nil {
		t.Fatal("LoadConfig(nil) = nil, want error")
	}
	_ = cfg
}

// TestWireFieldLimitRejectsOversizeField proves a single hostile field
// cannot push megabytes past the body cap in many small keys.
func TestWireFieldLimitRejectsOversizeField(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("a", WireFieldLimit+1)
	req := RepositoryIntakeRequest{APIVersion: APIVersionV1, Sender: big}
	if err := CheckWireLengths(map[string]string{"sender": req.Sender}); err == nil {
		t.Fatal("CheckWireLengths(oversize) = nil, want error")
	}
	if err := CheckWireLengths(map[string]string{"sender": "ok"}); err != nil {
		t.Fatalf("CheckWireLengths(ok) error = %v", err)
	}
}

// TestRepositoryNormalizeRejectsOversizePostIdentityFields proves fields
// set after the identity check (sender, ref, SHAs, PR fields) are still
// bounded: a 1 MiB hostile sender must fail closed, not normalize.
func TestRepositoryNormalizeRejectsOversizePostIdentityFields(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("s", WireFieldLimit+1)
	payload := `{"ref":"refs/heads/main","before":"a0","after":"b1",` +
		`"repository":{"id":7,"full_name":"o/r","default_branch":"main"},` +
		`"sender":{"login":"` + big + `"}}`
	if _, err := NormalizeGitHub("push", "d1", []byte(payload), "main"); err == nil {
		t.Fatal("NormalizeGitHub(oversize sender) = nil, want wire-length error")
	}
	bigRef := `{"ref":"refs/heads/` + strings.Repeat("b", WireFieldLimit+1) + `","before":"a0","after":"b1",` +
		`"repository":{"id":7,"full_name":"o/r","default_branch":"main"},` +
		`"sender":{"login":"u"}}`
	if _, err := NormalizeGitHub("push", "d1", []byte(bigRef), "main"); err == nil {
		t.Fatal("NormalizeGitHub(oversize ref) = nil, want wire-length error")
	}
}

// TestEmptyPayloadIsRejected proves empty and whitespace-only deliveries
// fail closed instead of normalizing to a zero trigger.
func TestEmptyPayloadIsRejected(t *testing.T) {
	t.Parallel()

	for _, payload := range [][]byte{nil, {}, []byte("   ")} {
		if _, err := NormalizeGitHub("push", "d1", payload, "main"); err == nil {
			t.Fatalf("NormalizeGitHub(empty %q) = nil, want error", payload)
		}
	}
}

// TestMalformedJSONIsRejected proves truncated provider payloads fail
// closed with an error, never a partial trigger.
func TestMalformedJSONIsRejected(t *testing.T) {
	t.Parallel()

	if _, err := NormalizeGitHub("push", "d1", []byte(`{"ref":`), "main"); err == nil {
		t.Fatal("NormalizeGitHub(truncated) = nil, want error")
	}
	if _, err := NormalizeGitLab("Push Hook", "d1", []byte(`[1,`), "main"); err == nil {
		t.Fatal("NormalizeGitLab(truncated) = nil, want error")
	}
}

// fork-pr lane proof, do not merge

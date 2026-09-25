package publicapi_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/config"
)

type failingYAMLValue struct{ err error }

func (v failingYAMLValue) MarshalYAML() (any, error) { return nil, v.err }

type panickingYAMLValue struct{ value any }

func (v panickingYAMLValue) MarshalYAML() (any, error) { panic(v.value) }

type removingYAMLValue struct{ path string }

func (v removingYAMLValue) MarshalYAML() (any, error) {
	if err := os.Remove(v.path); err != nil {
		return nil, err
	}
	return "new", nil
}

func TestPublicAPIConfigReturnsCallerYAMLMarshalErrorsWithoutWriting(t *testing.T) {
	wantErr := errors.New("value refused YAML encoding")
	for _, tc := range []struct {
		name    string
		body    string
		keyPath []string
	}{
		{"nested value", "name: app\nsettings:\n  enabled: true\n", []string{"settings", "enabled"}},
		{"whole document", "name: app\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "dispat.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o640); err != nil {
				t.Fatal(err)
			}
			err := config.ApplyEdits(context.Background(), path, []config.Edit{{
				KeyPath: tc.keyPath,
				Value:   failingYAMLValue{err: wantErr},
			}})
			if !errors.Is(err, wantErr) {
				t.Fatalf("ApplyEdits error = %v, want caller error", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != tc.body {
				t.Fatalf("failed marshal changed config to %q", got)
			}
			if _, statErr := os.Stat(path + config.BackupSuffix); !os.IsNotExist(statErr) {
				t.Fatalf("failed marshal created backup: %v", statErr)
			}
		})
	}
}

func TestPublicAPIConfigDoesNotConvertCallerYAMLPanicsIntoErrors(t *testing.T) {
	marker := &struct{ label string }{"caller panic"}
	dir := t.TempDir()
	path := filepath.Join(dir, "dispat.yaml")
	const body = "name: app\n"
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}

	func() {
		defer func() {
			if recovered := recover(); recovered != marker {
				t.Fatalf("recovered = %#v, want caller panic marker", recovered)
			}
		}()
		_ = config.ApplyEdits(context.Background(), path, []config.Edit{{
			KeyPath: []string{"name"},
			Value:   panickingYAMLValue{value: marker},
		}})
		t.Fatal("caller panic was swallowed")
	}()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("panic changed config to %q", got)
	}
	if _, err := os.Stat(path + config.BackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("panic created backup: %v", err)
	}
}

func TestPublicAPIConfigRejectsTruncatedNestedJSONWithoutWriting(t *testing.T) {
	cases := []struct {
		name, body string
		keyPath    []string
	}{
		{"truncated skipped value", `{"unrelated":[1`, []string{"target"}},
		{"truncated nested object", `{"target":`, []string{"target", "child"}},
		{"truncated target value", `{"target":`, []string{"target"}},
		{"missing closing object", `{"unrelated":1`, []string{"target"}},
		{"truncated key", `{"unrelated"`, []string{"target"}},
		{"invalid key after comma", `{"unrelated":1, :`, []string{"target"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "dispat.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			err := config.ApplyEdits(context.Background(), path, []config.Edit{{KeyPath: tc.keyPath, Value: "new"}})
			if err == nil {
				t.Fatal("malformed JSON edit succeeded")
			}
			if tc.name == "invalid key after comma" {
				if !strings.Contains(err.Error(), "object key string") {
					t.Fatalf("ApplyEdits error = %v, want invalid object key", err)
				}
			} else if !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "unexpected EOF") {
				t.Fatalf("ApplyEdits error = %v, want an EOF parsing error", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != tc.body {
				t.Fatalf("failed edit changed config to %q", got)
			}
			if _, statErr := os.Stat(path + config.BackupSuffix); !os.IsNotExist(statErr) {
				t.Fatalf("failed edit created backup: %v", statErr)
			}
		})
	}
}

func TestPublicAPIConfigPrepareReportsDestinationDisappearingDuringMarshal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dispat.yaml")
	if err := os.WriteFile(path, []byte("name: app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.PrepareEdits(context.Background(), path, []config.Edit{{
		KeyPath: []string{"name"}, Value: removingYAMLValue{path: path},
	}})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PrepareEdits error = %v, want destination disappearance", err)
	}
	if _, statErr := os.Stat(path + config.BackupSuffix); !os.IsNotExist(statErr) {
		t.Fatalf("failed preparation created backup: %v", statErr)
	}
}

func TestPublicAPIConfigPreparedCommitRefusesAChangedDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dispat.json")
	const body = `{"name":"app","logLevel":"info"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := config.PrepareEdits(context.Background(), path, []config.Edit{{
		KeyPath: []string{"logLevel"}, Value: "debug",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	// The commit refuses the changed destination by its type before it saves
	// the backup, as it refuses a symbolic link, so a refused commit writes
	// nothing at all rather than a backup of a file that is no longer there.
	err = prepared.Commit()
	if err == nil || !strings.Contains(err.Error(), "refusing to rewrite a non-regular file") {
		t.Fatalf("commit = %v, want a non-regular refusal", err)
	}
	info, statErr := os.Stat(path)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("changed destination was not preserved: info=%v err=%v", info, statErr)
	}
	if _, err := os.Lstat(path + config.BackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("refused commit wrote a backup: %v", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, "dispat.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("failed commit left temporary files: %v", leftovers)
	}
}

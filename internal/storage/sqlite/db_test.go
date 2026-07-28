package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func filePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%q): %v", path, err)
	}
	return info.Mode().Perm()
}

type openDSNCase struct {
	name         string
	dsn          func(string) string
	expectedFile string
	memory       bool
	seed         bool
}

func TestOpen_DSNMatrixUsesSQLiteResolvedPath(t *testing.T) {
	tests := []openDSNCase{
		{name: "plain.db", dsn: func(string) string { return "plain.db" }, expectedFile: "plain.db"},
		{name: "plain.db?cache=shared", dsn: func(string) string { return "plain.db?cache=shared" }, expectedFile: "plain.db"},
		{name: "prag.db?_pragma=busy_timeout(5000)", dsn: func(string) string {
			return "prag.db?_pragma=busy_timeout(5000)"
		}, expectedFile: "prag.db"},
		{name: "bug.db?mode=rw", dsn: func(string) string { return "bug.db?mode=rw" }, expectedFile: "bug.db"},
		{name: "file:rel.db", dsn: func(string) string { return "file:rel.db" }, expectedFile: "rel.db"},
		{name: "file:/abs.db", dsn: func(dir string) string {
			return "file:" + filepath.Join(dir, "abs-single.db")
		}, expectedFile: "abs-single.db"},
		{name: "file:///abs.db", dsn: func(dir string) string {
			return "file://" + filepath.Join(dir, "abs-triple.db")
		}, expectedFile: "abs-triple.db"},
		{name: "file://localhost/abs.db", dsn: func(dir string) string {
			return "file://localhost" + filepath.Join(dir, "abs-localhost.db")
		}, expectedFile: "abs-localhost.db"},
		{name: "file:a%20b.db", dsn: func(string) string { return "file:a%20b.db" }, expectedFile: "a b.db"},
		{name: "file:frag.db#zzz", dsn: func(string) string { return "file:frag.db#zzz" }, expectedFile: "frag.db"},
		{name: "file:pre.db?mode=memory&mode=rw", dsn: func(string) string {
			return "file:pre.db?mode=memory&mode=rw"
		}, expectedFile: "pre.db", seed: true},
		{name: "file:pre.db?mode=rw&mode=memory", dsn: func(string) string {
			return "file:pre.db?mode=rw&mode=memory"
		}, memory: true},
		{name: memoryDSN, dsn: func(string) string { return memoryDSN }, memory: true},
		{name: ":memory:?cache=shared", dsn: func(string) string { return ":memory:?cache=shared" }, memory: true},
		{name: "file:x.db?mode=memory", dsn: func(string) string { return "file:x.db?mode=memory" }, memory: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runOpenDSNCase(t, tt)
		})
	}
}

func runOpenDSNCase(t *testing.T, tt openDSNCase) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if tt.seed {
		seedPath := filepath.Join(dir, tt.expectedFile)
		if err := os.WriteFile(seedPath, nil, 0o600); err != nil {
			t.Fatalf("seed %q: %v", seedPath, err)
		}
	}

	dsn := tt.dsn(dir)
	db, err := Open(context.Background(), dsn, "")
	if err != nil {
		t.Fatalf("Open(%q): %v", dsn, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	mainPath, err := mainDatabasePath(context.Background(), db.conn)
	if err != nil {
		t.Fatalf("mainDatabasePath: %v", err)
	}
	assertDSNMainPathAndModes(t, tt, dir, mainPath)
	assertNoDecoyFiles(t, dir, dsn, mainPath)
}

func assertDSNMainPathAndModes(t *testing.T, tt openDSNCase, dir, mainPath string) {
	t.Helper()
	if tt.memory {
		if mainPath != "" {
			t.Fatalf("PRAGMA database_list main path = %q, want empty for memory database", mainPath)
		}
		return
	}
	expectedPath := filepath.Join(dir, tt.expectedFile)
	resolvedExpectedPath, err := filepath.EvalSymlinks(expectedPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", expectedPath, err)
	}
	if mainPath != resolvedExpectedPath {
		t.Fatalf("PRAGMA database_list main path = %q, want %q", mainPath, resolvedExpectedPath)
	}
	for _, path := range []string{mainPath, mainPath + "-wal", mainPath + "-shm"} {
		if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			t.Fatalf("Lstat(%q): %v", path, statErr)
		}
		if perm := filePerm(t, path); perm != 0o600 {
			t.Errorf("%q mode = %04o, want 0600", path, perm)
		}
	}
}

func assertNoDecoyFiles(t *testing.T, dir, dsn, mainPath string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	allowed := map[string]bool{}
	if mainPath != "" {
		allowed[filepath.Base(mainPath)] = true
		allowed[filepath.Base(mainPath)+"-wal"] = true
		allowed[filepath.Base(mainPath)+"-shm"] = true
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			t.Errorf("unexpected decoy file %q for DSN %q; PRAGMA main path is %q", entry.Name(), dsn, mainPath)
		}
	}
}

func TestOpen_TightensPreExistingMainAndSidecars(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "permissive.db")
	first, err := Open(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatalf("seed %q: %v", path, err)
			}
		}
		//nolint:gosec // G302: deliberately reproduces a pre-fix permissive database and sidecars.
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("make %q permissive: %v", path, err)
		}
	}

	reopened, err := Open(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if perm := filePerm(t, path); perm != 0o600 {
			t.Errorf("%q mode = %04o, want 0600", path, perm)
		}
	}
}

func TestOpenSQLiteConnection_NewFileStartsOwnerOnly(t *testing.T) {
	tests := []struct {
		name         string
		dsn          func(string) string
		expectedFile string
	}{
		{
			name:         "plain driver filename",
			dsn:          func(dir string) string { return filepath.Join(dir, "plain.db") },
			expectedFile: "plain.db",
		},
		{
			name:         "SQLite file URI",
			dsn:          func(dir string) string { return "file:" + filepath.Join(dir, "uri.db") },
			expectedFile: "uri.db",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			dsn := tt.dsn(dir)
			expectedPath := filepath.Join(dir, tt.expectedFile)

			stop := make(chan struct{})
			observed := make(chan os.FileMode)
			go func() {
				var bad os.FileMode
				for {
					select {
					case <-stop:
						observed <- bad
						return
					default:
					}
					if info, err := os.Lstat(expectedPath); err == nil && info.Mode().Perm() != 0o600 {
						bad = info.Mode().Perm()
					}
				}
			}()

			conn, mainPath, err := openSQLiteConnection(context.Background(), dsn)
			close(stop)
			badMode := <-observed
			if err != nil {
				t.Fatalf("openSQLiteConnection(%q): %v", dsn, err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
			})
			resolvedExpectedPath, err := filepath.EvalSymlinks(expectedPath)
			if err != nil {
				t.Fatalf("EvalSymlinks(%q): %v", expectedPath, err)
			}
			if mainPath != resolvedExpectedPath {
				t.Fatalf("PRAGMA database_list main path = %q, want %q", mainPath, resolvedExpectedPath)
			}
			if perm := filePerm(t, mainPath); perm != 0o600 {
				t.Errorf("mode immediately after Ping and before chmod = %04o, want 0600", perm)
			}
			if badMode != 0 {
				t.Errorf("watcher observed new DB at mode %04o before chmod", badMode)
			}
		})
	}
}

func TestSecureCreationDSN_MemoryNeverCreatesModeReference(t *testing.T) {
	for _, dsn := range []string{
		memoryDSN,
		":memory:?cache=shared",
		"file:pre.db?mode=rw&mode=memory",
		"file:x.db?mode=memory",
	} {
		t.Run(dsn, func(t *testing.T) {
			factoryCalled := false
			openDSN, reference, err := secureCreationDSNWith(
				dsn,
				func() (*os.File, string, error) {
					factoryCalled = true
					return nil, "", errors.New("mode-reference factory must not run for memory DSN")
				},
			)
			if err != nil {
				t.Fatalf("secureCreationDSNWith(%q): %v", dsn, err)
			}
			if factoryCalled {
				t.Error("mode-reference factory ran for memory DSN")
			}
			if reference != nil {
				t.Errorf("mode reference = %v, want nil", reference)
			}
			if openDSN != dsn {
				t.Errorf("open DSN = %q, want unchanged %q", openDSN, dsn)
			}
		})
	}
}

func TestOpen_RefusesSymlinks(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.db")
	seed, err := Open(context.Background(), realPath, "")
	if err != nil {
		t.Fatalf("seed Open: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("seed Close: %v", err)
	}

	for _, tt := range []struct {
		name string
		dsn  func(string) string
	}{
		{name: "plain main DSN", dsn: func(path string) string { return path }},
		{name: "file URI main DSN", dsn: func(path string) string { return "file:" + path }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			linkPath := filepath.Join(dir, tt.name+".db")
			if err := os.Symlink(realPath, linkPath); err != nil {
				t.Fatalf("Symlink: %v", err)
			}
			if db, err := Open(context.Background(), tt.dsn(linkPath), ""); err == nil {
				_ = db.Close()
				t.Fatal("Open(symlink DSN) succeeded, want hard error")
			} else if !strings.Contains(err.Error(), "refusing SQLite DSN symlink") {
				t.Fatalf("Open(symlink DSN) error = %v, want explicit symlink refusal", err)
			}
		})
	}

	t.Run("WAL sibling", func(t *testing.T) {
		victimPath := filepath.Join(dir, "victim")
		//nolint:gosec // G306: deliberately permissive victim proves chmod never follows the WAL symlink.
		if err := os.WriteFile(victimPath, []byte("unchanged"), 0o644); err != nil {
			t.Fatalf("seed victim: %v", err)
		}
		walPath := realPath + "-wal"
		if err := os.Remove(walPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remove old WAL: %v", err)
		}
		if err := os.Symlink(victimPath, walPath); err != nil {
			t.Fatalf("Symlink WAL: %v", err)
		}
		if db, err := Open(context.Background(), realPath, ""); err == nil {
			_ = db.Close()
			t.Fatal("Open(DB with symlink WAL) succeeded, want hard error")
		}
		if perm := filePerm(t, victimPath); perm != 0o644 {
			t.Errorf("symlink target mode changed to %04o, want 0644", perm)
		}
	})
}

func TestChmodOwnerOnlyWith_ToleratesFailureOnlyForObservedSafeMode(t *testing.T) {
	injected := errors.New("injected chmod failure")
	tests := []struct {
		mode    os.FileMode
		wantErr bool
	}{
		{mode: 0o600},
		{mode: 0o400},
		{mode: 0o640, wantErr: true},
		{mode: 0o644, wantErr: true},
		{mode: 0o666, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if err := os.Chmod(path, tt.mode); err != nil {
				t.Fatalf("seed mode %04o: %v", tt.mode, err)
			}

			err := chmodOwnerOnlyWith(path, false, func(path string, _ os.FileMode) error {
				// Change the live file to 0600 before returning an error. A
				// vulnerable post-failure re-stat would see this new safe mode
				// and incorrectly tolerate initially-permissive files.
				if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
					t.Fatalf("injected chmod setup: %v", chmodErr)
				}
				return injected
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("chmodOwnerOnlyWith(mode=%04o) error = %v, wantErr=%v", tt.mode, err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, injected) {
				t.Errorf("error = %v, want errors.Is(injected)=true", err)
			}
		})
	}
}

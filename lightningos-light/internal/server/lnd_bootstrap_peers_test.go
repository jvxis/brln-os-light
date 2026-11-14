package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLNDTemplateDoesNotRequireBootstrapPeersAtStartup(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "templates", "lnd.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range lndConfigEntries(string(raw)) {
		if entry.key == "addpeer" {
			t.Error("new native installations must not depend on an addpeer startup connection")
		}
	}
	if strings.Count(string(raw), "gossip.pinned-syncers=") != 2 {
		t.Fatal("unrelated gossip preferences changed")
	}
}

func bootstrapPeersFixture() []byte {
	return []byte("[Application Options]\nalias=Operator Node\n" +
		"addpeer=" + legacyLNDBootstrapPeers[0] + "\n" +
		"addpeer=" + legacyLNDBootstrapPeers[1] + "\n" +
		"addpeer=02operator@custom.onion:9735\n" +
		"wallet-unlock-password-file=/srv/custom/password\n" +
		"restlisten=127.0.0.1:8080\nrpclisten=127.0.0.1:10009\n" +
		"[Bitcoind]\nbitcoind.rpcpass=fixture-private-value\n" +
		"[db]\ndb.backend=bolt\n" +
		"[gossip]\ngossip.pinned-syncers=" + strings.Split(legacyLNDBootstrapPeers[1], "@")[0] + "\n")
}

func TestDisableLegacyLNDBootstrapPeersPreservesEveryOtherByte(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		for _, trailing := range []bool{false, true} {
			t.Run(fmt.Sprintf("eol-%q-trailing-%t", eol, trailing), func(t *testing.T) {
				raw := strings.ReplaceAll(string(bootstrapPeersFixture()), "\n", eol)
				if !trailing {
					raw = strings.TrimSuffix(raw, eol)
				}
				expected := raw
				for _, peer := range legacyLNDBootstrapPeers {
					expected = strings.ReplaceAll(expected, "addpeer="+peer, "#ddpeer="+peer)
				}
				got, count := disableLegacyLNDBootstrapPeers([]byte(raw))
				if count != 2 || string(got) != expected || len(got) != len(raw) {
					t.Fatal("migration changed more than the two default option key bytes")
				}
				if again, count := disableLegacyLNDBootstrapPeers(got); count != 0 || !bytes.Equal(got, again) {
					t.Fatal("comment transformation is not idempotent")
				}
			})
		}
	}
}

func TestDisableLegacyLNDBootstrapPeersMatchesExactValuesAndSections(t *testing.T) {
	peer := legacyLNDBootstrapPeers[0]
	raw := "addpeer=" + peer + "\n[Application Options]\n\tAddPeer = \"" + peer + "\" \r\n" +
		"addpeer=" + peer + "\n" +
		"#addpeer=" + peer + "\n;addpeer=" + peer + "\n" +
		"addpeer=" + strings.Replace(peer, ":9735", ":9736", 1) + "\n" +
		"addpeer=" + strings.Split(peer, "@")[0] + "@operator.onion:9735\n" +
		"[other]\naddpeer=" + peer + "\n"
	got, count := disableLegacyLNDBootstrapPeers([]byte(raw))
	expected := strings.Replace(raw, "addpeer="+peer+"\n", "#ddpeer="+peer+"\n", 1)
	expected = strings.Replace(expected, "\tAddPeer", "\t#ddPeer", 1)
	index := strings.Index(expected, "\r\naddpeer="+peer)
	expected = expected[:index] + strings.Replace(expected[index:], "\r\naddpeer=", "\r\n#ddpeer=", 1)
	if count != 3 || string(got) != expected {
		t.Fatal("exact default/section matching changed an operator option or comment")
	}
}

func newBootstrapMigrationFixture(t *testing.T) (lndBootstrapPeerMigration, []byte) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "lnd.conf")
	raw := bootstrapPeersFixture()
	if err := os.WriteFile(path, raw, 0660); err != nil {
		t.Fatal(err)
	}
	return lndBootstrapPeerMigration{configPath: path, backupDir: filepath.Join(root, "migration-private")}, raw
}

func TestLNDBootstrapPeerMigrationBacksUpAndPreservesFileIdentity(t *testing.T) {
	migration, original := newBootstrapMigrationFixture(t)
	before, _ := os.Stat(migration.configPath)
	count, err := migration.run(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("migration: count=%d error=%v", count, err)
	}
	want, _ := disableLegacyLNDBootstrapPeers(original)
	stored, _ := os.ReadFile(migration.configPath)
	after, _ := os.Stat(migration.configPath)
	if !bytes.Equal(stored, want) || !os.SameFile(before, after) || before.Mode() != after.Mode() {
		t.Fatal("migration replaced the inode, permissions or unrelated config bytes")
	}
	backup := filepath.Join(migration.backupDir, fmt.Sprintf("default-addpeer-v1-%x.conf", sha256.Sum256(original)))
	retained, err := os.ReadFile(backup)
	if err != nil || !bytes.Equal(retained, original) {
		t.Fatal("original was not privately backed up")
	}
	if runtime.GOOS == "linux" {
		info, _ := os.Stat(backup)
		dir, _ := os.Stat(migration.backupDir)
		if info.Mode().Perm() != 0600 || dir.Mode().Perm() != 0700 {
			t.Fatal("migration backup is not private")
		}
	}
	if count, err := migration.run(context.Background()); err != nil || count != 0 {
		t.Fatal("repeated migration was not a no-op")
	}
	// Once completed, do not keep deleting an operator's explicit later choice.
	if err := os.WriteFile(migration.configPath, original, before.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if count, err := migration.run(context.Background()); err != nil || count != 0 {
		t.Fatal("completed migration ran again after operator edit")
	}
	stored, _ = os.ReadFile(migration.configPath)
	if !bytes.Equal(stored, original) {
		t.Fatal("explicit post-migration operator choice was overwritten")
	}
}

func TestLNDBootstrapPeerMigrationDoesNothingWithoutDefaults(t *testing.T) {
	for _, mode := range []string{"missing", "no-defaults", "already-commented"} {
		t.Run(mode, func(t *testing.T) {
			migration, raw := newBootstrapMigrationFixture(t)
			if mode == "missing" {
				if err := os.Remove(migration.configPath); err != nil {
					t.Fatal(err)
				}
			} else {
				if mode == "no-defaults" {
					raw = []byte("[Application Options]\naddpeer=02operator@custom.onion:9735\n")
				} else {
					raw, _ = disableLegacyLNDBootstrapPeers(raw)
				}
				if err := os.WriteFile(migration.configPath, raw, 0660); err != nil {
					t.Fatal(err)
				}
			}
			if count, err := migration.run(context.Background()); err != nil || count != 0 {
				t.Fatalf("no-op migration: %d/%v", count, err)
			}
			if _, err := os.Lstat(migration.backupDir); !os.IsNotExist(err) {
				t.Fatal("no-op migration created backup/state")
			}
		})
	}
}

func TestLNDBootstrapPeerMigrationRefusesUnsafeStateWithoutEditingConfig(t *testing.T) {
	for _, mode := range []string{"backup-dir-file", "incomplete-backup", "unsafe-policy", "oversized", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			migration, raw := newBootstrapMigrationFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "backup-dir-file":
				if err := os.WriteFile(migration.backupDir, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			case "incomplete-backup":
				if err := os.Mkdir(migration.backupDir, 0700); err != nil {
					t.Fatal(err)
				}
				backup := filepath.Join(migration.backupDir, fmt.Sprintf("default-addpeer-v1-%x.conf", sha256.Sum256(raw)))
				if err := os.WriteFile(backup, []byte("partial"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unsafe-policy":
				raw = append(raw, []byte("\n[Application Options]\nno-macaroons=true\n")...)
			case "oversized":
				raw = append(raw, bytes.Repeat([]byte("#"), lndBootstrapPeersConfigLimit)...)
			case "cancelled":
				cancel()
			}
			if err := os.WriteFile(migration.configPath, raw, 0660); err != nil {
				t.Fatal(err)
			}
			if _, err := migration.run(ctx); err == nil {
				t.Fatal("unsafe/failed migration was accepted")
			}
			stored, _ := os.ReadFile(migration.configPath)
			if !bytes.Equal(stored, raw) {
				t.Fatal("failed preflight modified the LND config")
			}
		})
	}
}

func TestLNDBootstrapPeerMigrationInterruptionLeavesValidConfigAndCanRetry(t *testing.T) {
	migration, raw := newBootstrapMigrationFixture(t)
	migration.patch = func(path string, before, after []byte) error {
		// Simulate process/storage interruption after exactly one comment byte.
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		for offset := range before {
			if before[offset] != after[offset] {
				if _, err := file.WriteAt([]byte{'#'}, int64(offset)); err != nil {
					return err
				}
				break
			}
		}
		if err := file.Sync(); err != nil {
			return err
		}
		return errors.New("injected interruption")
	}
	if _, err := migration.run(context.Background()); err == nil {
		t.Fatal("injected interruption was ignored")
	}
	partial, _ := os.ReadFile(migration.configPath)
	if len(partial) != len(raw) {
		t.Fatal("partial migration truncated the file")
	}
	expected := bytes.Clone(raw)
	offset := bytes.Index(raw, []byte("addpeer="))
	expected[offset] = '#'
	if !bytes.Equal(partial, expected) {
		t.Fatal("interrupted sparse write changed other settings")
	}
	migration.patch = nil
	if count, err := migration.run(context.Background()); err != nil || count != 1 {
		t.Fatalf("interrupted cleanup retry: %d/%v", count, err)
	}
	want, _ := disableLegacyLNDBootstrapPeers(raw)
	stored, _ := os.ReadFile(migration.configPath)
	if !bytes.Equal(stored, want) {
		t.Fatal("retry did not finish the exact cleanup")
	}
}

func TestLNDBootstrapPeerSparseWriterRejectsStaleOrUnrelatedChanges(t *testing.T) {
	for _, mode := range []string{"stale", "different-option", "length", "false-success"} {
		t.Run(mode, func(t *testing.T) {
			migration, raw := newBootstrapMigrationFixture(t)
			want, _ := disableLegacyLNDBootstrapPeers(raw)
			switch mode {
			case "stale":
				changed := bytes.Replace(raw, []byte("Operator Node"), []byte("External Edit"), 1)
				if err := os.WriteFile(migration.configPath, changed, 0660); err != nil {
					t.Fatal(err)
				}
			case "different-option":
				want = bytes.Replace(want, []byte("Operator Node"), []byte("Attacker Node"), 1)
			case "length":
				want = append(want, '\n')
			case "false-success":
				migration.patch = func(string, []byte, []byte) error { return nil }
				if _, err := migration.run(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatal("readback failed to catch no-op writer")
				}
				return
			}
			before, _ := os.ReadFile(migration.configPath)
			identity, _ := os.Stat(migration.configPath)
			if err := writeLNDBootstrapPeerComments(migration.configPath, raw, want, identity); err == nil {
				t.Fatal("sparse writer accepted stale or unrelated edits")
			}
			after, _ := os.ReadFile(migration.configPath)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected sparse write modified config")
			}
		})
	}
}

func TestLNDBootstrapPeerMigrationSupportsExistingDirectoryLinksButNotLeafLinks(t *testing.T) {
	for _, leaf := range []bool{false, true} {
		t.Run(fmt.Sprint(leaf), func(t *testing.T) {
			migration, original := newBootstrapMigrationFixture(t)
			root := filepath.Dir(migration.configPath)
			link := filepath.Join(root, "existing-lnd")
			if leaf {
				link = filepath.Join(root, "linked-lnd.conf")
				if err := os.Symlink(migration.configPath, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else {
				if err := os.Symlink(root, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				link = filepath.Join(link, "lnd.conf")
			}
			migration.configPath = link
			count, err := migration.run(context.Background())
			if leaf {
				if err == nil {
					t.Fatal("linked config leaf was mutated automatically")
				}
				stored, _ := os.ReadFile(migration.configPath)
				if !bytes.Equal(stored, original) {
					t.Fatal("linked config target changed")
				}
			} else if err != nil || count != 2 {
				t.Fatalf("existing-node directory mapping was rejected: %d/%v", count, err)
			}
		})
	}
}

func TestLNDBootstrapPeerMigrationHasNoServiceOrPeerOperations(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "lnd_bootstrap_peers.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range file.Imports {
		if strings.Contains(imported.Path.Value, "os/exec") || strings.Contains(imported.Path.Value, "/system") || strings.Contains(imported.Path.Value, "/privileged") {
			t.Fatal("bootstrap cleanup gained service execution authority")
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				switch selector.Sel.Name {
				case "Chown", "Chmod", "Truncate", "ConnectPeer", "RestartService":
					t.Error("cleanup changes metadata, truncates config or controls services/peers")
				}
			}
		}
		return true
	})
}

func TestLNDBootstrapPeerMigrationRejectsLinkedBackupState(t *testing.T) {
	for _, kind := range []string{"directory", "backup", "completion"} {
		t.Run(kind, func(t *testing.T) {
			migration, raw := newBootstrapMigrationFixture(t)
			root := filepath.Dir(migration.configPath)
			var target, link string
			if kind == "directory" {
				target = filepath.Join(root, "unrelated-directory")
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				link = migration.backupDir
			} else {
				if err := os.Mkdir(migration.backupDir, 0700); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(root, "unrelated-file")
				content := raw
				name := fmt.Sprintf("default-addpeer-v1-%x.conf", sha256.Sum256(raw))
				if kind == "completion" {
					content = []byte("done\n")
					name = fmt.Sprintf("default-addpeer-v1-%x.done", sha256.Sum256([]byte(migration.configPath)))
				}
				if err := os.WriteFile(target, content, 0600); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(migration.backupDir, name)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if _, err := migration.run(context.Background()); err == nil {
				t.Fatal("symlinked migration state was accepted")
			}
			stored, _ := os.ReadFile(migration.configPath)
			if !bytes.Equal(stored, raw) {
				t.Fatal("linked state caused configuration edits")
			}
		})
	}
}

func TestLNDBootstrapPeerMigrationRunsFromNewManagerStartup(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "Run" || function.Recv == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					if selector.Sel.Name == "reconcileLNDBootstrapPeers" {
						found = true
					}
					if selector.Sel.Name == "startSystemIntegrationReconciler" && !found {
						t.Error("LND config migration must run before concurrent initializers")
					}
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("migration depends on a new upgrade helper instead of the installed Manager startup")
	}
}

package managed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func storedFixture(session string) Profile {
	data := "proxies: []\n# private fixture bytes\n"
	digest := sha256.Sum256([]byte(data))
	version := hex.EncodeToString(digest[:])
	return Profile{YAML: data, Version: version, Session: Status{SessionID: session, ProfileVersion: version}}
}

func assertStoredAbsent(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(home, ManagedDirectory, managedActiveFile)); !os.IsNotExist(err) {
		t.Fatal("managed bytes survived cleanup")
	}
}

func TestManagedStoreReplacesOneAtomicOwnedBundleAndCleansOnlyItsFiles(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(legacy, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	owner := ConfigurationOwner{Generation: 1, UserID: "a", SessionID: "session-a"}
	id, err := StoreConfiguration(home, owner, storedFixture(owner.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	owner = ConfigurationOwner{Generation: 2, UserID: "b", SessionID: "session-b"}
	next, err := StoreConfiguration(home, owner, storedFixture(owner.SessionID))
	if err != nil || next == id {
		t.Fatal("replacement did not rotate configuration identity")
	}
	namespace := filepath.Join(home, ManagedDirectory)
	data, err := os.ReadFile(filepath.Join(namespace, managedActiveFile))
	if err != nil {
		t.Fatal(err)
	}
	var record StoredConfiguration
	if json.Unmarshal(data, &record) != nil || record.Owner != owner || record.ID != next || record.YAML != storedFixture(owner.SessionID).YAML {
		t.Fatal("mixed or partial configuration bundle")
	}
	for _, file := range []string{"notes.txt", ".pending-not-owned.json", ".pending-" + strings.Repeat("a", 32) + ".json"} {
		if err = os.WriteFile(filepath.Join(namespace, file), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = ClearStoredConfiguration(home); err != nil {
		t.Fatal(err)
	}
	assertStoredAbsent(t, home)
	if err = ClearStoredConfiguration(home); err != nil {
		t.Fatal("cleanup is not idempotent")
	}
	for _, file := range []string{"notes.txt", ".pending-not-owned.json", "owner"} {
		if _, err = os.Stat(filepath.Join(namespace, file)); err != nil {
			t.Fatal("cleanup deleted an unrelated file")
		}
	}
	if _, err = os.Stat(filepath.Join(namespace, ".pending-"+strings.Repeat("a", 32)+".json")); !os.IsNotExist(err) {
		t.Fatal("owned crash temporary survived")
	}
	if data, _ = os.ReadFile(legacy); string(data) != "legacy" {
		t.Fatal("cleanup touched a legacy profile")
	}
}

func TestManagedStoreRejectsUnownedNamespaceAndSymlinkEscape(t *testing.T) {
	for _, kind := range []string{"unmarked", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			home, outside := t.TempDir(), t.TempDir()
			namespace := filepath.Join(home, ManagedDirectory)
			if kind == "symlink" {
				if err := os.Symlink(outside, namespace); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(namespace, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outside, "active.json"), []byte("unrelated"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := StoreConfiguration(home, ConfigurationOwner{1, "a", "session-a"}, storedFixture("session-a")); PublicError(err) != "configuration_storage_failed" {
				t.Fatal("unowned namespace accepted")
			}
			if err := ClearStoredConfiguration(home); PublicError(err) != "configuration_cleanup_failed" {
				t.Fatal("unowned cleanup accepted")
			}
			if data, _ := os.ReadFile(filepath.Join(outside, "active.json")); string(data) != "unrelated" {
				t.Fatal("cleanup escaped its namespace")
			}
		})
	}
}

func TestManagedStoreRenameFailureLeavesNoPartialFile(t *testing.T) {
	home := t.TempDir()
	root, err := openManagedDirectory(home, true)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	active := filepath.Join(home, ManagedDirectory, managedActiveFile)
	if err = os.Mkdir(active, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(active, "unrelated"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = StoreConfiguration(home, ConfigurationOwner{1, "a", "session-a"}, storedFixture("session-a")); PublicError(err) != "configuration_storage_failed" {
		t.Fatal("rename failure not surfaced")
	}
	entries, _ := os.ReadDir(filepath.Join(home, ManagedDirectory))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			t.Fatal("partial temporary survived failure")
		}
	}
	if err = ClearStoredConfiguration(home); PublicError(err) != "configuration_cleanup_failed" {
		t.Fatal("cleanup recursively removed unexpected contents")
	}
	if data, _ := os.ReadFile(filepath.Join(active, "unrelated")); string(data) != "preserve" {
		t.Fatal("unexpected directory content removed")
	}
}

func TestManagedStoreDoesNotFollowActiveFileSymlinks(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	root, err := openManagedDirectory(home, true)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	victim := filepath.Join(outside, "keep")
	if err = os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(home, ManagedDirectory, managedActiveFile)
	if err = os.Symlink(victim, active); err != nil {
		t.Fatal(err)
	}
	if _, err = StoreConfiguration(home, ConfigurationOwner{1, "a", "session-a"}, storedFixture("session-a")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Fatal("atomic replacement followed symlink")
	}
	if err = ClearStoredConfiguration(home); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(victim, active); err != nil {
		t.Fatal(err)
	}
	if err = ClearStoredConfiguration(home); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Fatal("cleanup followed symlink")
	}
}

func TestManagedStoreRejectsHashAndOwnerMismatchBeforeWriting(t *testing.T) {
	for _, owner := range []ConfigurationOwner{{0, "a", "session-a"}, {1, "", "session-a"}, {1, "a", "other-session"}} {
		home := t.TempDir()
		if _, err := StoreConfiguration(home, owner, storedFixture("session-a")); PublicError(err) != "invalid_client_config" {
			t.Fatal("bad owner accepted")
		}
		entries, _ := os.ReadDir(home)
		if len(entries) != 0 {
			t.Fatal("bad owner wrote files")
		}
	}
	home := t.TempDir()
	profile := storedFixture("session-a")
	profile.YAML += "modified"
	if _, err := StoreConfiguration(home, ConfigurationOwner{1, "a", "session-a"}, profile); PublicError(err) != "invalid_client_config" {
		t.Fatal("bad hash accepted")
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatal("bad hash wrote files")
	}
}

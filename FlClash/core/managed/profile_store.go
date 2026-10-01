package managed

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
)

const ManagedDirectory = "asterlink-managed-v1"
const managedMarker = "asterlink-managed-configuration-v1\n"
const managedActiveFile = "active.json"

type StoredConfiguration struct {
	ID      string             `json:"id"`
	Owner   ConfigurationOwner `json:"owner"`
	Version string             `json:"version"`
	YAML    string             `json:"yaml"`
}

func openManagedDirectory(home string, create bool) (*os.Root, error) {
	parent, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	info, err := parent.Lstat(ManagedDirectory)
	created := false
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			return nil, nil
		}
		if err = parent.Mkdir(ManagedDirectory, 0700); err != nil {
			return nil, err
		}
		created = true
	} else if err != nil {
		return nil, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, &APIError{"configuration_storage_failed"}
	}
	root, err := parent.OpenRoot(ManagedDirectory)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			root.Close()
		}
	}()
	if created {
		file, openErr := root.OpenFile("owner", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if openErr != nil {
			return nil, openErr
		}
		_, writeErr := file.WriteString(managedMarker)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
			return nil, err
		}
	}
	markerInfo, err := root.Lstat("owner")
	if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Size() != int64(len(managedMarker)) {
		return nil, &APIError{"configuration_storage_failed"}
	}
	marker, err := root.ReadFile("owner")
	if err != nil || string(marker) != managedMarker {
		return nil, &APIError{"configuration_storage_failed"}
	}
	if err = parent.Chmod(ManagedDirectory, 0700); err != nil {
		return nil, err
	}
	ok = true
	return root, nil
}

func ClearStoredConfiguration(home string) error {
	root, err := openManagedDirectory(home, false)
	if err != nil {
		return &APIError{"configuration_cleanup_failed"}
	}
	if root == nil {
		return nil
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return &APIError{"configuration_cleanup_failed"}
	}
	entries, err := directory.ReadDir(-1)
	closeErr := directory.Close()
	if errors.Join(err, closeErr) != nil {
		return &APIError{"configuration_cleanup_failed"}
	}
	for _, entry := range entries {
		name := entry.Name()
		pending := strings.TrimSuffix(strings.TrimPrefix(name, ".pending-"), ".json")
		_, decodeErr := hex.DecodeString(pending)
		isPending := len(pending) == 32 && decodeErr == nil && name == ".pending-"+pending+".json"
		if name != managedActiveFile && !isPending {
			continue
		}
		if err = root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return &APIError{"configuration_cleanup_failed"}
		}
	}
	return nil
}

func StoreConfiguration(home string, owner ConfigurationOwner, profile Profile) (string, error) {
	digest := sha256.Sum256([]byte(profile.YAML))
	if owner.UserID == "" || owner.SessionID == "" || owner.Generation == 0 || profile.Session.SessionID != owner.SessionID || hex.EncodeToString(digest[:]) != profile.Version {
		return "", &APIError{"invalid_client_config"}
	}
	root, err := openManagedDirectory(home, true)
	if err != nil {
		return "", &APIError{"configuration_storage_failed"}
	}
	defer root.Close()
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return "", &APIError{"configuration_storage_failed"}
	}
	key := hex.EncodeToString(id)
	data, err := json.Marshal(StoredConfiguration{key, owner, profile.Version, profile.YAML})
	if err != nil {
		return "", &APIError{"configuration_storage_failed"}
	}
	temp := ".pending-" + key + ".json"
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", &APIError{"configuration_storage_failed"}
	}
	defer root.Remove(temp)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		return "", &APIError{"configuration_storage_failed"}
	}
	if err = root.Rename(temp, managedActiveFile); err != nil {
		return "", &APIError{"configuration_storage_failed"}
	}
	return key, nil
}

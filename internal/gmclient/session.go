// Package gmclient wraps go.mau.fi/mautrix-gmessages/pkg/libgm with session
// persistence and connection management for gmesg.
package gmclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
)

// DefaultProfile is used when no profile name is given. It's the plain,
// un-namespaced session path so existing pairings from before profiles
// existed keep working without migration.
const DefaultProfile = ""

// sessionFile is the on-disk representation of a paired session. It holds
// everything libgm needs to reconnect without re-pairing: auth tokens,
// device identity and the AES/HMAC keys negotiated during pairing.
type sessionFile struct {
	Auth     *libgm.AuthData `json:"auth"`
	PushKeys *libgm.PushKeys `json:"push_keys,omitempty"`
}

// baseDataDir returns the directory gmesg stores all profiles' data under,
// honoring XDG_DATA_HOME like the rest of the user's environment.
func baseDataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "gmesg"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "gmesg"), nil
}

// DataDir returns the directory a given profile's session and cache data
// lives in. Running gmesg concurrently as two different paired identities
// (e.g. one in the interactive TUI, one in the notification daemon) needs
// two different profiles -- reusing one session's auth token from two
// processes at once fights over a single long-poll connection with
// Google's servers and can corrupt the session file on save.
func DataDir(profile string) (string, error) {
	base, err := baseDataDir()
	if err != nil {
		return "", err
	}
	if profile == DefaultProfile {
		return base, nil
	}
	return filepath.Join(base, "profiles", profile), nil
}

// CacheDir returns the directory downloaded attachments are cached in for a
// given profile. Unlike the session file, this holds no secrets -- it's
// just a place external viewers (xdg-open and friends) can be pointed at,
// since they need a real file path rather than bytes in memory.
func CacheDir(profile string) (string, error) {
	dir, err := DataDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cache"), nil
}

func sessionPath(profile string) (string, error) {
	dir, err := DataDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.json"), nil
}

// HasSession reports whether a paired session is already persisted on disk
// for the given profile.
func HasSession(profile string) (bool, error) {
	path, err := sessionPath(profile)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// LoadSession reads the persisted AuthData and PushKeys from disk.
func LoadSession(profile string) (*libgm.AuthData, *libgm.PushKeys, error) {
	path, err := sessionPath(profile)
	if err != nil {
		return nil, nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read session file: %w", err)
	}
	var sf sessionFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, nil, fmt.Errorf("parse session file: %w", err)
	}
	return sf.Auth, sf.PushKeys, nil
}

// SaveSession persists AuthData and PushKeys to disk with owner-only
// permissions, since this file is as sensitive as a login cookie.
func SaveSession(profile string, auth *libgm.AuthData, pushKeys *libgm.PushKeys) error {
	dir, err := DataDir(profile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	raw, err := json.MarshalIndent(sessionFile{Auth: auth, PushKeys: pushKeys}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	path, err := sessionPath(profile)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}
	return nil
}

// DeleteSession removes the persisted session, forcing a fresh pairing.
func DeleteSession(profile string) error {
	path, err := sessionPath(profile)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove session file: %w", err)
	}
	return nil
}

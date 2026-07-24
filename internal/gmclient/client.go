package gmclient

import (
	"fmt"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
)

// New builds a libgm.Client for the given profile, loading a persisted
// session if one exists or starting fresh (unpaired) otherwise. The
// returned bool reports whether an existing session was loaded.
func New(logger zerolog.Logger, profile string) (*libgm.Client, bool, error) {
	has, err := HasSession(profile)
	if err != nil {
		return nil, false, fmt.Errorf("check for existing session: %w", err)
	}
	if !has {
		return libgm.NewClient(libgm.NewAuthData(), nil, logger), false, nil
	}
	auth, pushKeys, err := LoadSession(profile)
	if err != nil {
		return nil, false, fmt.Errorf("load session: %w", err)
	}
	return libgm.NewClient(auth, pushKeys, logger), true, nil
}

// Save persists the client's current auth state under profile, meant to be
// called after a successful pairing or whenever tokens are refreshed.
func Save(profile string, c *libgm.Client) error {
	return SaveSession(profile, c.AuthData, c.PushKeys)
}

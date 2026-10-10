// Package secret stores credentials in the OS keychain.
//
// Only the refresh token is secret and it never touches disk. The active
// account's handle is not a credential, so it lives in a small config file so
// that a fresh shell knows which keychain entry to open.
package secret

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

// Service is the keychain service name.
const Service = "tgl"

// ErrNotFound is returned when no credential is stored.
var ErrNotFound = errors.New("no stored credential")

// Credential is what we persist per account.
type Credential struct {
	Handle     string `json:"handle"`
	DID        string `json:"did"`
	PDS        string `json:"pds"`
	RefreshJwt string `json:"refreshJwt"`
}

// Store writes a credential to the keychain, keyed on the account DID.
func Store(c *Credential) error {
	if c.DID == "" {
		return fmt.Errorf("credential has no DID")
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := keyring.Set(Service, c.DID, string(b)); err != nil {
		return keychainErr(err)
	}
	return setActive(c.DID)
}

// Load reads the credential for did, or the active account when did is empty.
func Load(did string) (*Credential, error) {
	if did == "" {
		var err error
		did, err = Active()
		if err != nil {
			return nil, err
		}
	}
	s, err := keyring.Get(Service, did)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, keychainErr(err)
	}
	var c Credential
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return nil, fmt.Errorf("stored credential for %s is corrupt: %w", did, err)
	}
	return &c, nil
}

// Delete removes a stored credential.
func Delete(did string) error {
	if did == "" {
		var err error
		did, err = Active()
		if err != nil {
			return err
		}
	}
	if err := keyring.Delete(Service, did); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return keychainErr(err)
	}
	if a, _ := Active(); a == did {
		_ = clearActive()
	}
	return nil
}

func keychainErr(err error) error {
	return fmt.Errorf("OS keychain unavailable (%w).\n"+
		"On a headless machine, authenticate with the TANGLED_HANDLE and "+
		"TANGLED_APP_PASSWORD environment variables instead", err)
}

// --- active account pointer (not a secret) ---

type config struct {
	ActiveDID string `json:"activeDid"`
}

func configPath() (string, error) {
	if p := os.Getenv("TGL_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tgl", "config.json"), nil
}

// Active returns the DID of the active account.
func Active() (string, error) {
	p, err := configPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	var c config
	if err := json.Unmarshal(b, &c); err != nil {
		return "", ErrNotFound
	}
	if strings.TrimSpace(c.ActiveDID) == "" {
		return "", ErrNotFound
	}
	return c.ActiveDID, nil
}

func setActive(did string) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(config{ActiveDID: did})
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func clearActive() error {
	p, err := configPath()
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

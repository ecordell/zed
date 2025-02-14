package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/99designs/keyring"
	"github.com/jzelinskie/stringz"
	"github.com/rs/zerolog/log"
	"golang.org/x/term"
)

var (
	// DefaultTokenStore is the TokenStore that should be used unless otherwise
	// specified.
	DefaultTokenStore = KeychainTokenStore{}

	// ErrTokenNotFound is returned if there is no Config in a ConfigStore.
	ErrTokenNotFound = errors.New("token does not exist")

	// ErrMultipleTokens is returned if there are multiple tokens with the same
	// name.
	ErrMultipleTokens = errors.New("multiple tokens with the same name")
)

// DefaultToken creates a Token from input, filling any missing values in
// with the current context's defaults.
func DefaultToken(psystem, endpoint, tokenStr string) (Token, error) {
	// If all info is explicitly passed, short-circuit any trips to storage.
	if psystem != "" && endpoint != "" && tokenStr != "" {
		token := Token{
			System:   psystem,
			Endpoint: endpoint,
			Prefix:   "",
			Secret:   tokenStr,
		}
		return token, nil
	}

	token, err := CurrentToken(DefaultConfigStore, DefaultTokenStore)
	if err != nil {
		if errors.Is(err, ErrConfigNotFound) {
			return Token{}, errors.New("must first save a token: see `zed token save --help`")
		}
		return Token{}, err
	}

	token = Token{
		System:   stringz.DefaultEmpty(psystem, token.System),
		Endpoint: stringz.DefaultEmpty(endpoint, token.Endpoint),
		Prefix:   token.Prefix,
		Secret:   stringz.DefaultEmpty(tokenStr, token.Secret),
	}

	return token, nil
}

// Token represents an API Token and all of its metadata.
type Token struct {
	System   string
	Endpoint string
	Prefix   string
	Secret   string
}

// TokenStore is anything that can securely persist Tokens.
type TokenStore interface {
	List(revealTokens bool) ([]Token, error)
	Get(system string) (Token, error)
	Put(system, endpoint, secret string) error
	Delete(system string) error
}

const (
	keychainSvcName = "zed tokens"
	keyringFilename = "keyring.jwt"
	redactedMessage = "<redacted>"
)

// KeychainTokenStore implements TokenStore by using the OS keychain service,
// falling back to an encrypted JWT on disk if the OS has no keychain.
type KeychainTokenStore struct{}

var _ TokenStore = KeychainTokenStore{}

func openKeyring() (keyring.Keyring, error) {
	path, err := localConfigPath()
	if err != nil {
		return nil, err
	}

	return keyring.Open(keyring.Config{
		ServiceName: keychainSvcName,
		FileDir:     filepath.Join(path, keyringFilename),
		FilePasswordFunc: func(prompt string) (string, error) {
			if password, ok := os.LookupEnv("ZED_KEYRING_PASSWORD"); ok {
				return password, nil
			}

			fmt.Fprintf(os.Stderr, "%s: ", prompt)
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			if err != nil {
				return "", err
			}
			fmt.Println()
			return string(b), nil
		},
	})
}

func encodeLabel(prefix, endpoint string) string {
	return stringz.Join("@", prefix, endpoint)
}

func decodeLabel(label string) (prefix, endpoint string) {
	if err := stringz.SplitExact(label, "@", &prefix, &endpoint); err != nil {
		endpoint = label
	}
	return
}

func splitAPIToken(token string) (prefix, secret string) {
	exploded := strings.Split(token, "_")
	return strings.Join(exploded[:len(exploded)-1], "_"), exploded[len(exploded)-1]
}

// List returns all of the tokens in the keychain.
//
// If revealTokens is true, the Token.Secrets field will contain the secret.
func (ks KeychainTokenStore) List(revealTokens bool) ([]Token, error) {
	ring, err := openKeyring()
	if err != nil {
		return nil, err
	}

	keys, err := ring.Keys()
	if err != nil {
		return nil, err
	}

	var tokens []Token
	for _, key := range keys {
		item, err := ring.Get(key)
		if err != nil {
			return nil, err
		}

		prefix, endpoint := decodeLabel(item.Label)
		secret := redactedMessage
		if revealTokens {
			secret = string(item.Data)
		}

		tokens = append(tokens, Token{
			System:   item.Key,
			Endpoint: endpoint,
			Prefix:   prefix,
			Secret:   secret,
		})
	}

	return tokens, nil
}

// Get fetches a Token from the keychain.
func (ks KeychainTokenStore) Get(system string) (Token, error) {
	ring, err := openKeyring()
	if err != nil {
		return Token{}, err
	}

	item, err := ring.Get(system)
	if err != nil {
		if err == keyring.ErrKeyNotFound {
			return Token{}, ErrTokenNotFound
		}
		return Token{}, err
	}
	log.Trace().Interface("keychain item", item).Send()

	prefix, endpoint := decodeLabel(item.Label)
	return Token{
		System:   item.Key,
		Endpoint: endpoint,
		Prefix:   prefix,
		Secret:   string(item.Data),
	}, nil
}

// Put overwrites a Token in the keychain.
func (ks KeychainTokenStore) Put(system, endpoint, secret string) error {
	prefix, secret := splitAPIToken(secret)

	ring, err := openKeyring()
	if err != nil {
		return err
	}

	return ring.Set(keyring.Item{
		Key:   system,
		Data:  []byte(secret),
		Label: encodeLabel(prefix, endpoint),
	})
}

// Delete removes a Token from the keychain.
func (ks KeychainTokenStore) Delete(system string) error {
	ring, err := openKeyring()
	if err != nil {
		return err
	}

	return ring.Remove(system)
}

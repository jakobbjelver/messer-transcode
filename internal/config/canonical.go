package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Render is the exact bytes this app writes for a config: the parsed struct,
// two-space indented, one trailing newline.
//
// Two things follow from rendering the *struct* rather than copying the source
// file. Field order is stable (it is the declaration order, which is the order
// every config in this project has always had), and a hand edit that only
// reorders keys or changes indentation is not a change at all — which is what
// makes "dirty" mean something.
func Render(c *Config) ([]byte, error) {
	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Fingerprint is the identity of a config document: parse it (which validates
// and applies defaults), render it, hash the bytes. Same config → same
// fingerprint, whatever the formatting.
func Fingerprint(doc []byte) (string, error) {
	c, err := Decode(doc)
	if err != nil {
		return "", err
	}
	rendered, err := Render(c)
	if err != nil {
		return "", err
	}
	return Hash(rendered), nil
}

// Hash is the sha256 of bytes, hex encoded.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// DecodeStrict is Decode with the error wrapped so a caller can tell "not JSON"
// from "valid JSON that breaks a rule".
func DecodeStrict(doc []byte) (*Config, error) {
	c, err := Decode(doc)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return c, nil
}

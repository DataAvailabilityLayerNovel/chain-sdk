package cosmoswasm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// NamespaceSize is the total byte length of a Celestia namespace (version + 28-byte ID).
	NamespaceSize = 29
	// namespaceV0PrefixSize is the number of leading zero bytes required in version-0 namespace IDs.
	namespaceV0PrefixSize = 18
	// namespaceV0DataSize is the number of usable bytes in a version-0 namespace ID.
	namespaceV0DataSize = 10
)

// Namespace represents a Celestia namespace for blob isolation.
// Each app-chain should use a unique namespace so its blobs don't collide
// with other chains on the same DA layer.
type Namespace struct {
	// Version is the namespace version (currently only 0 is standard).
	Version uint8
	// ID is the 28-byte namespace identifier.
	ID [28]byte
}

// NewNamespaceV0 creates a version-0 namespace from up to 10 bytes of data.
// The first 18 bytes of the ID are zero-padded per Celestia v0 rules.
func NewNamespaceV0(data []byte) (*Namespace, error) {
	if len(data) > namespaceV0DataSize {
		return nil, fmt.Errorf("namespace data too long: got %d bytes, max %d", len(data), namespaceV0DataSize)
	}

	ns := &Namespace{Version: 0}
	copy(ns.ID[namespaceV0PrefixSize:], data)
	return ns, nil
}

// NamespaceFromString deterministically derives a version-0 namespace from a
// human-readable string (e.g. "my-game-chain", "defi-app").
// This is the recommended way for app developers to pick a namespace.
func NamespaceFromString(s string) *Namespace {
	hash := sha256.Sum256([]byte(s))
	ns, _ := NewNamespaceV0(hash[:namespaceV0DataSize])
	return ns
}

// NamespaceFromHex parses a hex-encoded namespace (with or without 0x prefix).
func NamespaceFromHex(hexStr string) (*Namespace, error) {
	hexStr = strings.TrimPrefix(hexStr, "0x")

	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}

	if len(b) != NamespaceSize {
		return nil, fmt.Errorf("invalid namespace size: expected %d bytes, got %d", NamespaceSize, len(b))
	}

	ns := &Namespace{Version: b[0]}
	copy(ns.ID[:], b[1:])

	if ns.Version == 0 {
		for i := range namespaceV0PrefixSize {
			if ns.ID[i] != 0 {
				return nil, fmt.Errorf("invalid v0 namespace: first %d bytes of ID must be zero", namespaceV0PrefixSize)
			}
		}
	}

	return ns, nil
}

// Bytes returns the 29-byte wire representation (version + ID).
func (n *Namespace) Bytes() []byte {
	result := make([]byte, NamespaceSize)
	result[0] = n.Version
	copy(result[1:], n.ID[:])
	return result
}

// Hex returns the 0x-prefixed hex encoding.
func (n *Namespace) Hex() string {
	return "0x" + hex.EncodeToString(n.Bytes())
}

// String returns the hex representation for display.
func (n *Namespace) String() string {
	return n.Hex()
}

// Equal checks if two namespaces are identical.
func (n *Namespace) Equal(other *Namespace) bool {
	if n == nil || other == nil {
		return n == other
	}
	return n.Version == other.Version && n.ID == other.ID
}

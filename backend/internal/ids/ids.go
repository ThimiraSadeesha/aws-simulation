// Package ids generates AWS-style identifiers and secrets.
package ids

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"strings"
)

func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)[:n]
}

// New returns prefix + "-" + 17 hex chars, e.g. i-0a1b2c3d4e5f60718.
func New(prefix string) string { return prefix + "-" + randHex(17) }

const upperAlnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

func randFrom(alphabet string, n int) string {
	var sb strings.Builder
	max := big.NewInt(int64(len(alphabet)))
	for i := 0; i < n; i++ {
		x, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		sb.WriteByte(alphabet[x.Int64()])
	}
	return sb.String()
}

// AccessKeyID returns AKIA… (long-term) or ASIA… (temporary) style ids.
func AccessKeyID(temporary bool) string {
	if temporary {
		return "ASIA" + randFrom(upperAlnum, 16)
	}
	return "AKIA" + randFrom(upperAlnum, 16)
}

// Secret returns a 40-char URL-safe random secret.
func Secret() string {
	b := make([]byte, 30)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Token returns a 256-bit random hex token.
func Token() string { return randHex(64) }

// AccountID returns a random 12-digit account id.
func AccountID() string { return randFrom("0123456789", 12) }

// Unique returns an IAM-style unique id with the given 4-letter prefix (AIDA, AGPA, AROA, ANPA).
func Unique(prefix string) string { return prefix + randFrom(upperAlnum, 17) }

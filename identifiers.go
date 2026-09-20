package lobby

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"math/big"
	"time"
)

// SystemClock is the production UTC clock. Tests can inject their own Clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

// CryptoIdentifiers uses the operating system's cryptographic random source.
// Importing the package does not read randomness; each method reads on demand.
type CryptoIdentifiers struct{}

func (CryptoIdentifiers) Bytes(count int) ([]byte, error) {
	if count < 0 {
		return nil, NewError(CodeInvalidInput, "random byte count must not be negative")
	}
	result := make([]byte, count)
	if _, err := io.ReadFull(rand.Reader, result); err != nil {
		return nil, err
	}
	return result, nil
}
func (source CryptoIdentifiers) UUID() (string, error) {
	random, err := source.Bytes(16)
	if err != nil {
		return "", err
	}
	random[6] = (random[6] & 0x0f) | 0x40
	random[8] = (random[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(random)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
func (CryptoIdentifiers) JoinCode() (string, error) {
	result := make([]byte, JoinCodeLength)
	maximum := big.NewInt(int64(len(JoinCodeAlphabet)))
	for index := range result {
		// rand.Int uses rejection sampling, keeping the alphabet distribution unbiased.
		choice, err := rand.Int(rand.Reader, maximum)
		if err != nil {
			return "", err
		}
		result[index] = JoinCodeAlphabet[choice.Int64()]
	}
	return string(result), nil
}

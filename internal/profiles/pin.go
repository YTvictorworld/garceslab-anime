package profiles

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (OWASP minimum: m=19 MiB, t=2, p=1). Cheap enough for the
// mini PC, and PINs are additionally rate limited by the lockout.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// Lockout policy: after MaxPinFailures wrong PINs the profile is locked for PinLockDuration.
const MaxPinFailures = 5

// ValidatePinFormat checks that a PIN is 4 to 6 ASCII digits.
func ValidatePinFormat(pin string) error {
	if len(pin) < 4 || len(pin) > 6 {
		return ErrInvalidPin
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return ErrInvalidPin
		}
	}
	return nil
}

// HashPin returns a PHC-formatted argon2id hash of the PIN.
func HashPin(pin string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pin), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPin checks a PIN against a hash produced by HashPin (parameters are read from the hash).
func VerifyPin(pin, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pin), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

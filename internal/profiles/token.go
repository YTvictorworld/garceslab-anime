package profiles

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MaxTokenTTL is the hard upper bound of a profile session (§3.2).
const MaxTokenTTL = 24 * time.Hour

const tokenPrefix = "gp1"

// tokenSigner signs profile session tokens:
//
//	token = "gp1.<profileId>.<expiryUnix>.<base64url(HMAC-SHA256(secret, "profile:<id>:<clientId>:<tokenVersion>:<expiry>"))>"
//
// The client id and token version are not in the token: they are bound through the
// signature, so a token copied to another client, or issued before a PIN change, fails.
type tokenSigner struct {
	secret []byte
}

func (s tokenSigner) payload(profileID int64, clientID string, tokenVersion int64, expiry int64) []byte {
	return fmt.Appendf(nil, "profile:%d:%s:%d:%d", profileID, clientID, tokenVersion, expiry)
}

func (s tokenSigner) sign(profileID int64, clientID string, tokenVersion int64, expiry int64) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(s.payload(profileID, clientID, tokenVersion, expiry))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s.%d.%d.%s", tokenPrefix, profileID, expiry, sig)
}

// parse extracts the profile id and expiry without checking the signature.
func (s tokenSigner) parse(token string) (profileID int64, expiry int64, err error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 4 || parts[0] != tokenPrefix || parts[3] == "" {
		return 0, 0, ErrInvalidToken
	}
	profileID, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil || profileID <= 0 {
		return 0, 0, ErrInvalidToken
	}
	expiry, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, 0, ErrInvalidToken
	}
	return profileID, expiry, nil
}

// verify checks the token against the current profile state and client id.
func (s tokenSigner) verify(token string, profileID int64, clientID string, tokenVersion int64, expiry int64, now time.Time) error {
	if expiry <= now.Unix() || expiry > now.Add(MaxTokenTTL).Unix() {
		return ErrInvalidToken
	}
	want := s.sign(profileID, clientID, tokenVersion, expiry)
	if !hmac.Equal([]byte(want), []byte(strings.TrimSpace(token))) {
		return ErrInvalidToken
	}
	return nil
}

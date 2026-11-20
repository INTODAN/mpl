package domain

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
)

// An account's display name is from MinAccountNameLength to
// MaxAccountNameLength characters after trimming.
const (
	MinAccountNameLength = 3
	MaxAccountNameLength = 24
)

// Account is a person who signed in, such as with Google, and so is known to
// the lobby across sessions and devices. It holds the display name the
// person chose, and nothing that the identity provider says about them but
// the subject that keys it.
type Account struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// AccountSession is the session that a sign-in opens: a guest session's
// fields, and the account whose player it is.
type AccountSession struct {
	GuestSession
	Account Account `json:"account"`
}

// SignInRequest signs in with a credential of the identity provider, such as
// a Google ID token. DisplayName names a new account and is ignored for a
// known one.
type SignInRequest struct {
	Credential  string `json:"credential"`
	DisplayName string `json:"display_name"`
}

// SignInSettings tells a client how to offer sign-in: the Google OAuth
// client ID its button uses, or empty while sign-in is off.
type SignInSettings struct {
	GoogleClientID string
}

// Accounts signs people in and manages the account of a signed-in session.
type Accounts interface {
	SignInSettings() SignInSettings
	// SignInWithGoogle verifies a Google ID token and opens a session of a
	// new player of the token's account, creating the account on its first
	// sign-in. While sign-in is off it returns ErrSignInOff.
	SignInWithGoogle(context.Context, SignInRequest) (AccountSession, error)
	// Account, RenameAccount and DeleteAccount act on the caller's account,
	// and return ErrSignInRequired for a guest.
	Account(context.Context, Principal) (Account, error)
	RenameAccount(context.Context, Principal, string) (Account, error)
	DeleteAccount(context.Context, Principal) error
}

// AccountStore persists accounts. Optional lookups return (nil, nil) only
// when absent.
type AccountStore interface {
	AccountBySubject(ctx context.Context, subject string) (*Account, error)
	AccountByID(ctx context.Context, id string) (*Account, error)
	// CreateAccountSession commits, together, the account when create is
	// true, a new player of it and the player's session. A name that another
	// account holds is ErrDisplayNameTaken, and a subject that another
	// account holds, which a concurrent first sign-in can cause,
	// ErrAccountExists.
	CreateAccountSession(ctx context.Context, account Account, subject string, create bool, player Player, session Session) error
	// RenameAccount renames the account and one of its players.
	RenameAccount(ctx context.Context, accountID, playerID, displayName string, updatedAt time.Time) error
	// DeleteAccount deletes the account and every session of its players.
	// The players stay, without an account.
	DeleteAccount(ctx context.Context, accountID string) error
}

// ErrInvalidAccountName refuses an account's display name that breaks
// NormalizeAccountName's rule. Its message is safe to show the client.
var ErrInvalidAccountName = NewError(CodeInvalidInput, "a display name is 3 to 24 characters of letters, digits, spaces, - and _")

// NormalizeAccountName trims an account's display name and checks it: from
// MinAccountNameLength to MaxAccountNameLength characters, of letters,
// digits, spaces, '-' and '_'. A guest's name keeps NormalizeName's rule.
func NormalizeAccountName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", ErrInvalidAccountName
	}
	name = strings.TrimSpace(name)
	if length := utf8.RuneCountInString(name); length < MinAccountNameLength || length > MaxAccountNameLength {
		return "", ErrInvalidAccountName
	}
	for _, character := range name {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != ' ' && character != '-' && character != '_' {
			return "", ErrInvalidAccountName
		}
	}
	return name, nil
}

// AccountNameKey is the key that makes account names unique without regard
// to case: the normalized name, case-folded.
func AccountNameKey(name string) string { return cases.Fold().String(name) }

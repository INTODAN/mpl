package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/INTODAN/mpl/domain"
)

// Accounts signs people in with Google and manages the account of a
// signed-in session. A sign-in opens a session as a guest session does, of a
// new player that belongs to the account.
type Accounts struct {
	sessions       *Service
	store          domain.AccountStore
	verifier       IDTokenVerifier
	googleClientID string
}

// NewAccounts manages accounts in store and opens their sessions with
// sessions. verifier checks Google ID tokens for googleClientID, the client
// ID that sign-in buttons use; a nil verifier turns sign-in off.
func NewAccounts(sessions *Service, store domain.AccountStore, verifier IDTokenVerifier, googleClientID string) (*Accounts, error) {
	if sessions == nil || store == nil {
		return nil, errors.New("lobby auth: missing dependency")
	}
	if verifier != nil && googleClientID == "" {
		return nil, errors.New("lobby auth: sign-in needs a Google client ID")
	}
	if verifier == nil {
		googleClientID = ""
	}
	return &Accounts{sessions: sessions, store: store, verifier: verifier, googleClientID: googleClientID}, nil
}

func (accounts *Accounts) SignInSettings() domain.SignInSettings {
	return domain.SignInSettings{GoogleClientID: accounts.googleClientID}
}

func (accounts *Accounts) SignInWithGoogle(ctx context.Context, request domain.SignInRequest) (domain.AccountSession, error) {
	if accounts.verifier == nil {
		return domain.AccountSession{}, domain.ErrSignInOff
	}
	if err := ctx.Err(); err != nil {
		return domain.AccountSession{}, unavailable(err)
	}
	subject, err := accounts.verifier.Verify(ctx, request.Credential)
	if err != nil {
		if domain.ErrorCodeOf(err) == "" {
			err = unavailable(err)
		}
		return domain.AccountSession{}, err
	}
	// A first sign-in that races another of the same account finds the
	// account on its second look, and one that races the account's deletion
	// finds none and creates a new account.
	for attempt := 0; attempt < 2; attempt++ {
		session, err := accounts.signIn(ctx, subject, request.DisplayName)
		if !errors.Is(err, domain.ErrAccountExists) && !errors.Is(err, domain.ErrAccountGone) {
			return session, err
		}
	}
	return domain.AccountSession{}, unavailable(errors.New("the account changed during every attempt to sign in"))
}

func (accounts *Accounts) signIn(ctx context.Context, subject, displayName string) (domain.AccountSession, error) {
	account, err := accounts.store.AccountBySubject(ctx, subject)
	if err != nil {
		return domain.AccountSession{}, unavailable(err)
	}
	create := account == nil
	credential, err := accounts.sessions.newCredential()
	if err != nil {
		return domain.AccountSession{}, err
	}
	if create {
		if strings.TrimSpace(displayName) == "" {
			return domain.AccountSession{}, domain.ErrDisplayNameRequired
		}
		name, err := domain.NormalizeAccountName(displayName)
		if err != nil {
			return domain.AccountSession{}, err
		}
		accountID, err := accounts.sessions.identifiers.UUID()
		if err != nil {
			return domain.AccountSession{}, unavailable(err)
		}
		// PostgreSQL keeps microseconds, so the answer matches later reads.
		account = &domain.Account{ID: accountID, DisplayName: name, CreatedAt: credential.session.CreatedAt.Truncate(time.Microsecond)}
	}
	player := domain.Player{ID: credential.session.PlayerID, DisplayName: account.DisplayName, AccountID: account.ID, CreatedAt: credential.session.CreatedAt}
	if err := accounts.store.CreateAccountSession(ctx, *account, subject, create, player, credential.session); err != nil {
		return domain.AccountSession{}, storeError(err)
	}
	return domain.AccountSession{GuestSession: credential.guestSession(), Account: *account}, nil
}

func (accounts *Accounts) Account(ctx context.Context, principal domain.Principal) (domain.Account, error) {
	if principal.AccountID == "" {
		return domain.Account{}, domain.ErrSignInRequired
	}
	account, err := accounts.store.AccountByID(ctx, principal.AccountID)
	if err != nil {
		return domain.Account{}, unavailable(err)
	}
	if account == nil {
		return domain.Account{}, domain.ErrSignInRequired
	}
	return *account, nil
}

func (accounts *Accounts) RenameAccount(ctx context.Context, principal domain.Principal, displayName string) (domain.Account, error) {
	if principal.AccountID == "" {
		return domain.Account{}, domain.ErrSignInRequired
	}
	name, err := domain.NormalizeAccountName(displayName)
	if err != nil {
		return domain.Account{}, err
	}
	if err := accounts.store.RenameAccount(ctx, principal.AccountID, principal.PlayerID, name, accounts.sessions.clock.Now()); err != nil {
		return domain.Account{}, storeError(err)
	}
	return accounts.Account(ctx, principal)
}

func (accounts *Accounts) DeleteAccount(ctx context.Context, principal domain.Principal) error {
	if principal.AccountID == "" {
		return domain.ErrSignInRequired
	}
	return storeError(accounts.store.DeleteAccount(ctx, principal.AccountID))
}

// storeError passes the domain's errors and ErrAccountExists through, and
// makes any other store error unavailable.
func storeError(err error) error {
	if err == nil || domain.ErrorCodeOf(err) != "" || errors.Is(err, domain.ErrAccountExists) || errors.Is(err, domain.ErrAccountGone) {
		return err
	}
	return unavailable(err)
}

var _ domain.Accounts = (*Accounts)(nil)

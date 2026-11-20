package httpapi

import (
	"context"
	"net/http"

	"github.com/INTODAN/mpl/domain"
)

func (handler handlers) signInSettings(_ context.Context, _ *SignInSettingsInput) (*SignInSettingsOutput, error) {
	var settings SignInSettings
	if handler.accounts != nil {
		if clientID := handler.accounts.SignInSettings().GoogleClientID; clientID != "" {
			settings.GoogleClientID = &clientID
		}
	}
	return &SignInSettingsOutput{Body: settings}, nil
}

func (handler handlers) googleSession(ctx context.Context, input *CreateGoogleSessionInput) (*CreateGoogleSessionOutput, error) {
	if handler.accounts == nil {
		return nil, signInOff()
	}
	result, err := handler.accounts.SignInWithGoogle(ctx, domain.SignInRequest{Credential: input.Body.Credential, DisplayName: input.Body.DisplayName})
	if err != nil {
		if err == domain.ErrSignInOff {
			return nil, signInOff()
		}
		return nil, problemFor(err)
	}
	return &CreateGoogleSessionOutput{Body: AccountSession{
		PlayerID: result.PlayerID, AccessToken: result.AccessToken, ExpiresAt: result.ExpiresAt, Account: accountFromDomain(result.Account),
	}}, nil
}

// signInOff answers a sign-in while the lobby has no sign-in configured.
func signInOff() error {
	return &Problem{Title: http.StatusText(http.StatusNotFound), Status: http.StatusNotFound, Detail: domain.ErrSignInOff.Message, Code: domain.CodeNotFound}
}

func (handler handlers) account(ctx context.Context, _ *AccountInput) (*AccountOutput, error) {
	if handler.accounts == nil {
		return nil, problemFor(domain.ErrSignInRequired)
	}
	result, err := handler.accounts.Account(ctx, principalFromContext(ctx))
	if err != nil {
		return nil, problemFor(err)
	}
	return &AccountOutput{Body: accountFromDomain(result)}, nil
}

func (handler handlers) updateAccount(ctx context.Context, input *UpdateAccountInput) (*AccountOutput, error) {
	if handler.accounts == nil {
		return nil, problemFor(domain.ErrSignInRequired)
	}
	result, err := handler.accounts.RenameAccount(ctx, principalFromContext(ctx), input.Body.DisplayName)
	if err != nil {
		return nil, problemFor(err)
	}
	return &AccountOutput{Body: accountFromDomain(result)}, nil
}

func (handler handlers) deleteAccount(ctx context.Context, _ *DeleteAccountInput) (*EmptyOutput, error) {
	if handler.accounts == nil {
		return nil, problemFor(domain.ErrSignInRequired)
	}
	if err := handler.accounts.DeleteAccount(ctx, principalFromContext(ctx)); err != nil {
		return nil, problemFor(err)
	}
	return &EmptyOutput{}, nil
}

func accountFromDomain(account domain.Account) Account {
	return Account{ID: account.ID, DisplayName: account.DisplayName, CreatedAt: account.CreatedAt}
}

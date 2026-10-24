package domain

// GameRegistration declares one game that may use the lobby. Every field is a
// declaration the lobby applies without knowing the game, and a zero field
// keeps the lobby's default behavior, so new options are added as new fields.
type GameRegistration struct {
	// Key is the canonical game key used in requests, URLs and channels.
	Key string
}

package domain

import "strings"

const JoinCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
const JoinCodeLength = 8

type ChannelNamespace string

const (
	ChannelLobby  ChannelNamespace = "lobby"
	ChannelPlayer ChannelNamespace = "player"
)

type Channel struct {
	Namespace ChannelNamespace
	Scope     Scope
	PlayerID  string
}

func ValidateGameKey(key string) error {
	if len(key) < 1 || len(key) > 32 || key[0] < 'a' || key[0] > 'z' {
		return NewError(CodeInvalidInput, "invalid game key")
	}
	for _, char := range key {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
			return NewError(CodeInvalidInput, "invalid game key")
		}
	}
	return nil
}

// ValidateUUID accepts canonical lowercase UUID text only, without coercion.
func ValidateUUID(value string) error {
	if len(value) != 36 {
		return NewError(CodeInvalidInput, "invalid UUID")
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if char != '-' {
				return NewError(CodeInvalidInput, "invalid UUID")
			}
			continue
		}
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return NewError(CodeInvalidInput, "invalid UUID")
		}
	}
	return nil
}
func RoomChannel(scope Scope) (string, error) {
	if err := ValidateGameKey(scope.GameKey); err != nil {
		return "", err
	}
	if err := ValidateUUID(scope.LobbyID); err != nil {
		return "", err
	}
	return "lobby:" + scope.GameKey + ":" + scope.LobbyID, nil
}
func PlayerChannel(playerID string) (string, error) {
	if err := ValidateUUID(playerID); err != nil {
		return "", err
	}
	return "player:" + playerID, nil
}
func ParseChannel(value string) (Channel, error) {
	segments := strings.Split(value, ":")
	if len(segments) == 3 && segments[0] == string(ChannelLobby) {
		scope := Scope{GameKey: segments[1], LobbyID: segments[2]}
		canonical, err := RoomChannel(scope)
		if err == nil && canonical == value {
			return Channel{Namespace: ChannelLobby, Scope: scope}, nil
		}
	}
	if len(segments) == 2 && segments[0] == string(ChannelPlayer) {
		canonical, err := PlayerChannel(segments[1])
		if err == nil && canonical == value {
			return Channel{Namespace: ChannelPlayer, PlayerID: segments[1]}, nil
		}
	}
	return Channel{}, NewError(CodeInvalidInput, "invalid channel")
}
func NormalizeJoinCode(code string) (string, error) {
	code = strings.Trim(code, " \t\n\r\v\f")
	if len(code) != JoinCodeLength {
		return "", NewError(CodeInvalidInput, "invalid join code")
	}
	normalized := []byte(code)
	for index, char := range normalized {
		if char >= 'a' && char <= 'z' {
			char -= 'a' - 'A'
			normalized[index] = char
		}
		if !strings.ContainsRune(JoinCodeAlphabet, rune(char)) {
			return "", NewError(CodeInvalidInput, "invalid join code")
		}
	}
	return string(normalized), nil
}

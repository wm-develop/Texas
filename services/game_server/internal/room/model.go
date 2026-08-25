package room

import "time"

type Preset string

const (
	PresetCasual   Preset = "casual"
	PresetStandard Preset = "standard"
	PresetDeep     Preset = "deep"
)

type Rules struct {
	StartingChips int64 `json:"startingChips"`
	SmallBlind    int64 `json:"smallBlind"`
	BigBlind      int64 `json:"bigBlind"`
	ActionSeconds int   `json:"actionSeconds"`
}

type Participant struct {
	UserID      string
	DisplayName string
}

type Member struct {
	UserID      string    `json:"userId"`
	DisplayName string    `json:"displayName"`
	Seat        int       `json:"seat"`
	Ready       bool      `json:"ready"`
	JoinedAt    time.Time `json:"joinedAt"`
}

type Room struct {
	RoomID       string    `json:"roomId"`
	Code         string    `json:"code"`
	OwnerUserID  string    `json:"ownerUserId"`
	Preset       Preset    `json:"preset"`
	Rules        Rules     `json:"rules"`
	MaxPlayers   int       `json:"maxPlayers"`
	Members      []Member  `json:"members"`
	Revision     uint64    `json:"revision"`
	CreatedAt    time.Time `json:"createdAt"`
	PasswordHash string    `json:"-"`
}

type Error struct {
	Code string
}

func (roomError Error) Error() string { return roomError.Code }

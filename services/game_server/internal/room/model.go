package room

import "time"

type Preset string

const (
	PresetCasual      Preset = "casual"
	PresetStandard    Preset = "standard"
	PresetDeep        Preset = "deep"
	MinimumSmallBlind int64  = 10
	MinimumBigBlind   int64  = 20
	MaximumPlayers           = 10
)

type Rules struct {
	StartingChips int64 `json:"startingChips"`
	MaxBuyIn      int64 `json:"maxBuyIn"`
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
	Stack       int64     `json:"stack"`
	JoinedAt    time.Time `json:"joinedAt"`
}

type Room struct {
	RoomID      string   `json:"roomId"`
	Code        string   `json:"code"`
	OwnerUserID string   `json:"ownerUserId"`
	Preset      Preset   `json:"preset"`
	Rules       Rules    `json:"rules"`
	MaxPlayers  int      `json:"maxPlayers"`
	Members     []Member `json:"members"`
	// JoinLocked 为 true 时房主已关闭入口，新玩家无法加入；已在房间内的
	// 成员不受影响。
	JoinLocked   bool      `json:"joinLocked"`
	Revision     uint64    `json:"revision"`
	CreatedAt    time.Time `json:"createdAt"`
	PasswordHash string    `json:"-"`
}

type Preview struct {
	Code             string `json:"code"`
	JoinLocked       bool   `json:"joinLocked"`
	Rules            Rules  `json:"rules"`
	MaxPlayers       int    `json:"maxPlayers"`
	CurrentPlayers   int    `json:"currentPlayers"`
	PasswordRequired bool   `json:"passwordRequired"`
}

type Error struct {
	Code string
}

func (roomError Error) Error() string { return roomError.Code }

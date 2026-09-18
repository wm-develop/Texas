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
	// MaximumSpectators 是观战位人数上限，与座位数无关。
	MaximumSpectators = 10
	// 观战看牌费按大盲倍数计，0 表示免费；上限防止房主误填掏空观战者。
	DefaultSpectatorFeeBigBlinds = 10
	MaximumSpectatorFeeBigBlinds = 100
)

// SpectatorSettings 是房主对观战位的约束。
//
// 观战者付费后能看到所有人的手牌，因此同时给房主关闭语音/文字/表情的开关：
// 熟人局里念底牌给同伴听是真实风险，是否默认放开由房主决定。
type SpectatorSettings struct {
	FeeBigBlinds int  `json:"feeBigBlinds"`
	VoiceAllowed bool `json:"voiceAllowed"`
	ChatAllowed  bool `json:"chatAllowed"`
	EmoteAllowed bool `json:"emoteAllowed"`
}

func DefaultSpectatorSettings() SpectatorSettings {
	return SpectatorSettings{
		FeeBigBlinds: DefaultSpectatorFeeBigBlinds,
		VoiceAllowed: true, ChatAllowed: true, EmoteAllowed: true,
	}
}

// Valid 报告设置是否在允许范围内。
func (settings SpectatorSettings) Valid() bool {
	return settings.FeeBigBlinds >= 0 && settings.FeeBigBlinds <= MaximumSpectatorFeeBigBlinds
}

// RakeSettings 是管理员为一个房间设置的抽水规则，新房间一律不抽水。
//
// 每手抽水 = min(底池 × BasisPoints / 10000 向下取整, Cap) + 翻后加抽。Cap 为 0 表示
// 比例部分不设上限；翻后加抽不受 Cap 限制，只在这手牌发出过翻牌时收取，且不得
// 超过一个大盲。抽走的筹码进管理员钱包。修改从下一手开始生效。
type RakeSettings struct {
	Enabled         bool  `json:"enabled"`
	BasisPoints     int   `json:"basisPoints"`
	Cap             int64 `json:"cap"`
	PostflopEnabled bool  `json:"postflopEnabled"`
	PostflopAmount  int64 `json:"postflopAmount"`
}

// MaximumRakeBasisPoints 是抽水比例的上限（10%）。
const MaximumRakeBasisPoints = 1000

// Valid 报告规则是否在允许范围内。
func (settings RakeSettings) Valid(bigBlind int64) bool {
	return settings.BasisPoints >= 0 && settings.BasisPoints <= MaximumRakeBasisPoints &&
		settings.Cap >= 0 && settings.Cap <= MaximumRakeCap &&
		settings.PostflopAmount >= 0 && settings.PostflopAmount <= bigBlind
}

// MaximumRakeCap 只是防止写入离谱的数；实际上限由管理员按房间盲注决定。
const MaximumRakeCap = 1_000_000_000

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
	// Spectating 为 true 表示该成员在观战位：不占座位（Seat 为 0）、不参与
	// 牌局，但仍是房间成员并持有牌桌筹码（用于支付看牌费与补码）。
	Spectating bool `json:"spectating"`
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
	JoinLocked bool `json:"joinLocked"`
	// Spectator 是房主对观战位的设置。
	Spectator SpectatorSettings `json:"spectator"`
	// Rake 是管理员为本房间设置的抽水规则。
	Rake         RakeSettings `json:"rake"`
	Revision     uint64       `json:"revision"`
	CreatedAt    time.Time    `json:"createdAt"`
	PasswordHash string       `json:"-"`
}

type Preview struct {
	Code       string `json:"code"`
	JoinLocked bool   `json:"joinLocked"`
	Rules      Rules  `json:"rules"`
	// Rake 让人在带入之前就知道这个房间抽不抽水、抽多少。
	Rake             RakeSettings `json:"rake"`
	MaxPlayers       int          `json:"maxPlayers"`
	CurrentPlayers   int          `json:"currentPlayers"`
	PasswordRequired bool         `json:"passwordRequired"`
}

type Error struct {
	Code string
}

func (roomError Error) Error() string { return roomError.Code }

// SeatedMembers 返回占有座位的成员，不含观战者。
func (value Room) SeatedMembers() []Member {
	result := make([]Member, 0, len(value.Members))
	for _, member := range value.Members {
		if !member.Spectating {
			result = append(result, member)
		}
	}
	return result
}

// SpectatorMembers 返回观战位上的成员。
func (value Room) SpectatorMembers() []Member {
	result := make([]Member, 0, len(value.Members))
	for _, member := range value.Members {
		if member.Spectating {
			result = append(result, member)
		}
	}
	return result
}

func seatedCount(members []Member) int {
	count := 0
	for _, member := range members {
		if !member.Spectating {
			count++
		}
	}
	return count
}

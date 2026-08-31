package room

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/security"
)

type ServiceConfig struct {
	Now      func() time.Time
	Random   io.Reader
	Bankroll *bankroll.Service
}

type Service struct {
	mu         sync.Mutex
	repository Repository
	passwords  *security.PasswordHasher
	config     ServiceConfig
	bankroll   *bankroll.Service
}

func NewService(repository Repository, passwords *security.PasswordHasher, config ServiceConfig) (*Service, error) {
	if repository == nil || passwords == nil {
		return nil, errors.New("invalid room service configuration")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = cryptorand.Reader
	}
	return &Service{repository: repository, passwords: passwords, config: config, bankroll: config.Bankroll}, nil
}

type CreateOptions struct {
	Preset     Preset
	MaxPlayers int
	Password   string
	SmallBlind int64
	BigBlind   int64
	MaxBuyIn   int64
	BuyIn      int64
	RequestID  string
}

type JoinOptions struct {
	Code      string
	Password  string
	BuyIn     int64
	RequestID string
}

func (service *Service) CreateConfigured(ctx context.Context, owner Participant, options CreateOptions) (Room, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.bankroll == nil || !validParticipant(owner) || options.MaxPlayers < 2 || options.MaxPlayers > 10 {
		return Room{}, Error{Code: "invalid_room"}
	}
	if current, err := service.repository.ByUser(ctx, owner.UserID); err == nil {
		if current.OwnerUserID == owner.UserID {
			return publicRoom(current), nil
		}
		return Room{}, Error{Code: "already_in_room"}
	} else if !errors.Is(err, ErrNotFound) {
		return Room{}, err
	}
	presetRules, ok := rulesForPreset(options.Preset)
	if !ok || !validBlindLevel(options.SmallBlind, options.BigBlind) ||
		options.MaxBuyIn < options.BigBlind || options.BuyIn <= 0 || options.BuyIn > options.MaxBuyIn {
		return Room{}, Error{Code: "invalid_room_rules"}
	}
	passwordHash, err := service.hashRoomPassword(options.Password)
	if err != nil {
		return Room{}, err
	}
	rules := Rules{
		StartingChips: options.MaxBuyIn, MaxBuyIn: options.MaxBuyIn,
		SmallBlind: options.SmallBlind, BigBlind: options.BigBlind,
		ActionSeconds: presetRules.ActionSeconds,
	}
	for attempt := 0; attempt < 10; attempt++ {
		roomID, err := service.randomID("table_", 9)
		if err != nil {
			return Room{}, err
		}
		code, err := service.randomCode()
		if err != nil {
			return Room{}, err
		}
		buyInRequestID := fmt.Sprintf("%s:%d", options.RequestID, attempt)
		if !bankroll.ValidRequestID(buyInRequestID) {
			return Room{}, Error{Code: "invalid_request"}
		}
		now := service.config.Now()
		value := Room{
			RoomID: roomID, Code: code, OwnerUserID: owner.UserID, Preset: options.Preset,
			Rules: rules, MaxPlayers: options.MaxPlayers, Revision: 1, CreatedAt: now,
			PasswordHash: passwordHash,
			Members:      []Member{{UserID: owner.UserID, DisplayName: owner.DisplayName, Seat: 1, Stack: options.BuyIn, JoinedAt: now}},
		}
		if atomic, ok := service.repository.(BuyInRepository); ok {
			err := atomic.CreateWithBuyIn(ctx, value, buyInRequestID, options.BuyIn, now)
			if err == nil {
				return publicRoom(value), nil
			}
			if !errors.Is(err, ErrConflict) {
				return Room{}, mapBankrollError(err)
			}
			continue
		}
		position, err := service.bankroll.BuyIn(ctx, owner.UserID, roomID, buyInRequestID, options.BuyIn, options.MaxBuyIn)
		if err != nil {
			return Room{}, mapBankrollError(err)
		}
		value.Members[0].Stack = position.TableChips
		if err := service.repository.Create(ctx, value); err == nil {
			return publicRoom(value), nil
		}
		_, _ = service.bankroll.CashOut(ctx, owner.UserID, roomID, "rollback:"+buyInRequestID)
	}
	return Room{}, errors.New("could not allocate unique room identifiers")
}

func (service *Service) JoinWithBuyIn(ctx context.Context, participant Participant, options JoinOptions) (Room, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.bankroll == nil || !validParticipant(participant) {
		return Room{}, Error{Code: "invalid_profile"}
	}
	if current, err := service.repository.ByUser(ctx, participant.UserID); err == nil {
		if current.Code == strings.TrimSpace(options.Code) {
			return publicRoom(current), nil
		}
		return Room{}, Error{Code: "already_in_room"}
	}
	value, err := service.repository.ByCode(ctx, strings.TrimSpace(options.Code))
	if err != nil {
		return Room{}, Error{Code: "room_not_found"}
	}
	if value.PasswordHash != "" && !service.passwords.Verify("room:"+options.Password, value.PasswordHash) {
		return Room{}, Error{Code: "invalid_room_password"}
	}
	if len(value.Members) >= value.MaxPlayers {
		return Room{}, Error{Code: "room_full"}
	}
	if options.BuyIn <= 0 || options.BuyIn > value.Rules.MaxBuyIn {
		return Room{}, Error{Code: "invalid_buy_in"}
	}
	if !bankroll.ValidRequestID(options.RequestID) {
		return Room{}, Error{Code: "invalid_request"}
	}
	member := Member{
		UserID: participant.UserID, DisplayName: participant.DisplayName,
		Stack: options.BuyIn, JoinedAt: service.config.Now(),
	}
	if atomic, ok := service.repository.(BuyInRepository); ok {
		joined, err := atomic.JoinWithBuyIn(ctx, value.RoomID, member, options.RequestID, options.BuyIn, member.JoinedAt)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				return Room{}, Error{Code: "already_in_room"}
			}
			return Room{}, mapBankrollError(err)
		}
		return publicRoom(joined), nil
	}
	position, err := service.bankroll.BuyIn(ctx, participant.UserID, value.RoomID, options.RequestID, options.BuyIn, value.Rules.MaxBuyIn)
	if err != nil {
		return Room{}, mapBankrollError(err)
	}
	seat := firstAvailableSeat(value.Members, value.MaxPlayers)
	member.Seat = seat
	member.Stack = position.TableChips
	value.Members = append(value.Members, member)
	sort.Slice(value.Members, func(left, right int) bool { return value.Members[left].Seat < value.Members[right].Seat })
	value.Revision++
	if err := service.repository.Save(ctx, value); err != nil {
		_, _ = service.bankroll.CashOut(ctx, participant.UserID, value.RoomID, "rollback:"+options.RequestID)
		return Room{}, err
	}
	return publicRoom(value), nil
}

func (service *Service) UpdateStacks(ctx context.Context, roomID string, stacks map[string]int64) (Room, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.repository.ByID(ctx, roomID)
	if err != nil {
		return Room{}, Error{Code: "room_not_found"}
	}
	for index := range value.Members {
		if stack, ok := stacks[value.Members[index].UserID]; ok {
			if stack < 0 {
				return Room{}, Error{Code: "invalid_table_balance"}
			}
			value.Members[index].Stack = stack
			if stack == 0 {
				value.Members[index].Ready = false
			}
		}
	}
	value.Revision++
	if err := service.repository.Save(ctx, value); err != nil {
		return Room{}, err
	}
	return publicRoom(value), nil
}

func (service *Service) AddToStack(ctx context.Context, roomID, userID string, amount int64) (Room, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.repository.ByID(ctx, roomID)
	if err != nil {
		return Room{}, Error{Code: "room_not_found"}
	}
	for index := range value.Members {
		if value.Members[index].UserID != userID {
			continue
		}
		if amount <= 0 || value.Members[index].Stack > value.Rules.MaxBuyIn-amount {
			return Room{}, Error{Code: "maximum_buy_in_exceeded"}
		}
		value.Members[index].Stack += amount
		value.Revision++
		if err := service.repository.Save(ctx, value); err != nil {
			return Room{}, err
		}
		return publicRoom(value), nil
	}
	return Room{}, Error{Code: "permission_denied"}
}

func (service *Service) SetStack(ctx context.Context, roomID, userID string, stack int64) (Room, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.repository.ByID(ctx, roomID)
	if err != nil {
		return Room{}, Error{Code: "room_not_found"}
	}
	for index := range value.Members {
		if value.Members[index].UserID != userID {
			continue
		}
		if stack < 0 || stack > value.Rules.MaxBuyIn {
			return Room{}, Error{Code: "maximum_buy_in_exceeded"}
		}
		if value.Members[index].Stack != stack {
			value.Members[index].Stack = stack
			value.Revision++
			if err := service.repository.Save(ctx, value); err != nil {
				return Room{}, err
			}
		}
		return publicRoom(value), nil
	}
	return Room{}, Error{Code: "permission_denied"}
}

func (service *Service) Create(
	ctx context.Context,
	owner Participant,
	preset Preset,
	maxPlayers int,
	password string,
) (Room, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if !validParticipant(owner) || maxPlayers < 2 || maxPlayers > 10 {
		return Room{}, Error{Code: "invalid_room"}
	}
	if _, err := service.repository.ByUser(ctx, owner.UserID); err == nil {
		return Room{}, Error{Code: "already_in_room"}
	} else if !errors.Is(err, ErrNotFound) {
		return Room{}, err
	}
	rules, ok := rulesForPreset(preset)
	if !ok {
		return Room{}, Error{Code: "invalid_room"}
	}
	passwordHash := ""
	password = strings.TrimSpace(password)
	if password != "" {
		if len(password) < 4 || len(password) > 32 {
			return Room{}, Error{Code: "invalid_room_password"}
		}
		var err error
		passwordHash, err = service.passwords.Hash("room:" + password)
		if err != nil {
			return Room{}, err
		}
	}

	for attempt := 0; attempt < 10; attempt++ {
		roomID, err := service.randomID("table_", 9)
		if err != nil {
			return Room{}, err
		}
		code, err := service.randomCode()
		if err != nil {
			return Room{}, err
		}
		now := service.config.Now()
		value := Room{
			RoomID:       roomID,
			Code:         code,
			OwnerUserID:  owner.UserID,
			Preset:       preset,
			Rules:        rules,
			MaxPlayers:   maxPlayers,
			Revision:     1,
			CreatedAt:    now,
			PasswordHash: passwordHash,
			Members: []Member{{
				UserID: owner.UserID, DisplayName: owner.DisplayName, Seat: 1, Stack: rules.StartingChips, JoinedAt: now,
			}},
		}
		if err := service.repository.Create(ctx, value); err == nil {
			return publicRoom(value), nil
		}
	}
	return Room{}, errors.New("could not allocate unique room identifiers")
}

func (service *Service) Join(
	ctx context.Context,
	participant Participant,
	code string,
	password string,
) (Room, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if !validParticipant(participant) {
		return Room{}, Error{Code: "invalid_profile"}
	}
	if current, err := service.repository.ByUser(ctx, participant.UserID); err == nil {
		if current.Code == strings.TrimSpace(code) {
			return publicRoom(current), nil
		}
		return Room{}, Error{Code: "already_in_room"}
	}
	value, err := service.repository.ByCode(ctx, strings.TrimSpace(code))
	if err != nil {
		return Room{}, Error{Code: "room_not_found"}
	}
	if value.PasswordHash != "" && !service.passwords.Verify("room:"+password, value.PasswordHash) {
		return Room{}, Error{Code: "invalid_room_password"}
	}
	if len(value.Members) >= value.MaxPlayers {
		return Room{}, Error{Code: "room_full"}
	}
	seat := firstAvailableSeat(value.Members, value.MaxPlayers)
	value.Members = append(value.Members, Member{
		UserID: participant.UserID, DisplayName: participant.DisplayName, Seat: seat, Stack: value.Rules.StartingChips, JoinedAt: service.config.Now(),
	})
	sort.Slice(value.Members, func(left, right int) bool { return value.Members[left].Seat < value.Members[right].Seat })
	value.Revision++
	if err := service.repository.Save(ctx, value); err != nil {
		return Room{}, err
	}
	return publicRoom(value), nil
}

func (service *Service) Current(ctx context.Context, userID string) (Room, error) {
	value, err := service.repository.ByUser(ctx, userID)
	if err != nil {
		return Room{}, Error{Code: "room_not_found"}
	}
	return publicRoom(value), nil
}

func (service *Service) Preview(ctx context.Context, code string) (Preview, error) {
	value, err := service.repository.ByCode(ctx, strings.TrimSpace(code))
	if err != nil {
		return Preview{}, Error{Code: "room_not_found"}
	}
	return Preview{
		Code: value.Code, Rules: value.Rules, MaxPlayers: value.MaxPlayers,
		CurrentPlayers: len(value.Members), PasswordRequired: value.PasswordHash != "",
	}, nil
}

func (service *Service) GetForMember(ctx context.Context, userID string, roomID string) (Room, error) {
	value, err := service.repository.ByID(ctx, roomID)
	if err != nil {
		return Room{}, Error{Code: "room_not_found"}
	}
	for _, member := range value.Members {
		if member.UserID == userID {
			return publicRoom(value), nil
		}
	}
	return Room{}, Error{Code: "permission_denied"}
}

func (service *Service) SetReady(ctx context.Context, userID string, ready bool) (Room, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.repository.ByUser(ctx, userID)
	if err != nil {
		return Room{}, Error{Code: "room_not_found"}
	}
	for index := range value.Members {
		if value.Members[index].UserID == userID {
			if ready && value.Members[index].Stack <= 0 {
				return Room{}, Error{Code: "rebuy_required"}
			}
			if value.Members[index].Ready != ready {
				value.Members[index].Ready = ready
				value.Revision++
			}
			if err := service.repository.Save(ctx, value); err != nil {
				return Room{}, err
			}
			return publicRoom(value), nil
		}
	}
	return Room{}, Error{Code: "permission_denied"}
}

func (service *Service) MoveSeat(ctx context.Context, userID string, targetSeat int) (Room, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.repository.ByUser(ctx, userID)
	if err != nil {
		return Room{}, Error{Code: "room_not_found"}
	}
	if targetSeat <= 0 || targetSeat > value.MaxPlayers {
		return Room{}, Error{Code: "invalid_seat"}
	}
	memberIndex := -1
	for index := range value.Members {
		if value.Members[index].Seat == targetSeat && value.Members[index].UserID != userID {
			return Room{}, Error{Code: "seat_occupied"}
		}
		if value.Members[index].UserID == userID {
			memberIndex = index
		}
	}
	if memberIndex < 0 {
		return Room{}, Error{Code: "permission_denied"}
	}
	if value.Members[memberIndex].Seat == targetSeat {
		return publicRoom(value), nil
	}
	value.Members[memberIndex].Seat = targetSeat
	sort.Slice(value.Members, func(left, right int) bool { return value.Members[left].Seat < value.Members[right].Seat })
	value.Revision++
	if err := service.repository.Save(ctx, value); err != nil {
		return Room{}, err
	}
	return publicRoom(value), nil
}

func (service *Service) SwapSeats(ctx context.Context, firstUserID, secondUserID string) (Room, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.repository.ByUser(ctx, firstUserID)
	if err != nil {
		return Room{}, Error{Code: "room_not_found"}
	}
	firstIndex, secondIndex := -1, -1
	for index := range value.Members {
		switch value.Members[index].UserID {
		case firstUserID:
			firstIndex = index
		case secondUserID:
			secondIndex = index
		}
	}
	if firstIndex < 0 || secondIndex < 0 || firstUserID == secondUserID {
		return Room{}, Error{Code: "invalid_seat_swap"}
	}
	value.Members[firstIndex].Seat, value.Members[secondIndex].Seat =
		value.Members[secondIndex].Seat, value.Members[firstIndex].Seat
	sort.Slice(value.Members, func(left, right int) bool { return value.Members[left].Seat < value.Members[right].Seat })
	value.Revision++
	if err := service.repository.Save(ctx, value); err != nil {
		return Room{}, err
	}
	return publicRoom(value), nil
}

func (service *Service) Leave(ctx context.Context, userID string) (closed bool, err error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.repository.ByUser(ctx, userID)
	if err != nil {
		return false, Error{Code: "room_not_found"}
	}
	for index, member := range value.Members {
		if member.UserID == userID {
			value.Members = append(value.Members[:index], value.Members[index+1:]...)
			if len(value.Members) == 0 {
				return true, service.repository.Delete(ctx, value.RoomID)
			}
			if value.OwnerUserID == userID {
				newOwner := value.Members[0]
				for _, candidate := range value.Members[1:] {
					if candidate.JoinedAt.Before(newOwner.JoinedAt) ||
						(candidate.JoinedAt.Equal(newOwner.JoinedAt) && candidate.Seat < newOwner.Seat) {
						newOwner = candidate
					}
				}
				value.OwnerUserID = newOwner.UserID
			}
			value.Revision++
			return false, service.repository.Save(ctx, value)
		}
	}
	return false, Error{Code: "permission_denied"}
}

func (service *Service) CanJoinVoice(ctx context.Context, userID string, tableID string) (bool, error) {
	value, err := service.repository.ByID(ctx, tableID)
	if err != nil {
		return false, nil
	}
	for _, member := range value.Members {
		if member.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func rulesForPreset(preset Preset) (Rules, bool) {
	switch preset {
	case PresetCasual:
		return Rules{StartingChips: 1000, MaxBuyIn: 1000, SmallBlind: 10, BigBlind: 20, ActionSeconds: 30}, true
	case PresetStandard:
		return Rules{StartingChips: 2000, MaxBuyIn: 2000, SmallBlind: 10, BigBlind: 20, ActionSeconds: 30}, true
	case PresetDeep:
		return Rules{StartingChips: 5000, MaxBuyIn: 5000, SmallBlind: 10, BigBlind: 20, ActionSeconds: 30}, true
	default:
		return Rules{}, false
	}
}

func validBlindLevel(smallBlind, bigBlind int64) bool {
	return smallBlind >= MinimumSmallBlind && bigBlind >= MinimumBigBlind &&
		bigBlind > smallBlind && bigBlind%smallBlind == 0
}

func (service *Service) hashRoomPassword(password string) (string, error) {
	password = strings.TrimSpace(password)
	if password == "" {
		return "", nil
	}
	if len(password) < 4 || len(password) > 32 {
		return "", Error{Code: "invalid_room_password"}
	}
	value, err := service.passwords.Hash("room:" + password)
	if err != nil {
		return "", err
	}
	return value, nil
}

func mapBankrollError(err error) error {
	var bankrollError bankroll.Error
	if errors.As(err, &bankrollError) {
		return Error{Code: bankrollError.Code}
	}
	return err
}

func validParticipant(participant Participant) bool {
	return participant.UserID != "" && strings.TrimSpace(participant.DisplayName) != ""
}

func firstAvailableSeat(members []Member, maxPlayers int) int {
	occupied := make(map[int]struct{}, len(members))
	for _, member := range members {
		occupied[member.Seat] = struct{}{}
	}
	for seat := 1; seat <= maxPlayers; seat++ {
		if _, exists := occupied[seat]; !exists {
			return seat
		}
	}
	return 0
}

func publicRoom(value Room) Room {
	value.PasswordHash = ""
	return cloneRoom(value)
}

func (service *Service) randomID(prefix string, byteCount int) (string, error) {
	value := make([]byte, byteCount)
	if _, err := io.ReadFull(service.config.Random, value); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(value), nil
}

func (service *Service) randomCode() (string, error) {
	value, err := cryptorand.Int(service.config.Random, big.NewInt(900_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()+100_000), nil
}

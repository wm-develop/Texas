package tablemanager

// 临时的真实模型试验：用真实牌桌管理层让几种风格的机器人打一段牌局，挑出本人
// 有代表性的几手交给配置的大模型复盘，把提示词与原始输出写到指定目录。只在设置
// REVIEW_LIVE_ENV（.env 路径）与 REVIEW_LIVE_OUT 时运行，会产生真实费用。

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/review"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
)

type liveStyle struct {
	name                     string
	open, call, reraise      float64
	bluff, aggression, loose float64
}

var liveStyles = map[string]liveStyle{
	"hero":    {name: "紧凶", open: 0.55, call: 0.62, reraise: 0.76, bluff: 0.12, aggression: 0.6, loose: 0.0},
	"fish":    {name: "松弱跟注站", open: 0.80, call: 0.30, reraise: 0.88, bluff: 0.02, aggression: 0.15, loose: 0.5},
	"maniac":  {name: "疯子", open: 0.30, call: 0.40, reraise: 0.55, bluff: 0.45, aggression: 0.8, loose: 0.3},
	"nit":     {name: "岩石", open: 0.72, call: 0.76, reraise: 0.86, bluff: 0.0, aggression: 0.4, loose: 0.0},
	"regular": {name: "常规", open: 0.52, call: 0.58, reraise: 0.72, bluff: 0.18, aggression: 0.5, loose: 0.1},
}

func preflopStrength(cards []string) float64 {
	hole := make([]holdem.Card, 0, 2)
	for _, value := range cards {
		card, err := holdem.ParseCard(value)
		if err != nil {
			return 0
		}
		hole = append(hole, card)
	}
	if len(hole) != 2 {
		return 0
	}
	high, low := float64(hole[0].Rank), float64(hole[1].Rank)
	if low > high {
		high, low = low, high
	}
	if high == low {
		return 0.55 + high/14*0.45
	}
	score := (high+low)/28*0.6 + (high/14)*0.15
	if hole[0].Suit == hole[1].Suit {
		score += 0.07
	}
	if high-low == 1 {
		score += 0.05
	} else if high-low == 2 {
		score += 0.02
	}
	return score
}

type postflopRead struct {
	category holdem.Category
	topPair  bool
	draw     bool
}

func readPostflop(hole, board []string) postflopRead {
	cards := make([]holdem.Card, 0, 7)
	for _, value := range append(append([]string{}, hole...), board...) {
		card, err := holdem.ParseCard(value)
		if err != nil {
			return postflopRead{}
		}
		cards = append(cards, card)
	}
	value, err := holdem.Evaluate(cards)
	if err != nil {
		return postflopRead{}
	}
	read := postflopRead{category: value.Category}
	var boardHigh holdem.Rank
	suits := map[holdem.Suit]int{}
	for index, card := range cards {
		suits[card.Suit]++
		if index >= 2 && card.Rank > boardHigh {
			boardHigh = card.Rank
		}
	}
	if value.Category == holdem.OnePair {
		for _, card := range cards[:2] {
			if card.Rank >= boardHigh {
				for _, other := range cards {
					if other != card && other.Rank == card.Rank {
						read.topPair = true
					}
				}
			}
		}
	}
	for _, count := range suits {
		if count == 4 && len(board) < 5 {
			read.draw = true
		}
	}
	return read
}

func roundTo(value, unit int64) int64 { return (value + unit/2) / unit * unit }

func liveAction(source *rand.Rand, style liveStyle, snapshot Snapshot, me SeatSnapshot) holdem.ActionRequest {
	options := snapshot.CurrentAction.Options
	request := holdem.ActionRequest{HandID: snapshot.HandID, TableRevision: snapshot.TableRevision}
	pot := snapshot.TotalPot
	var highest int64
	for _, seat := range snapshot.Seats {
		if seat.StreetBet > highest {
			highest = seat.StreetBet
		}
	}
	sized := func(target int64) holdem.ActionRequest {
		target = roundTo(target, 10)
		if target >= options.MaxRaiseTo || options.MaxRaiseTo < options.MinRaiseTo {
			request.Action = holdem.ActionAllIn
			return request
		}
		if target < options.MinRaiseTo {
			target = options.MinRaiseTo
		}
		if options.CanRaise {
			request.Action = holdem.ActionRaise
		} else {
			request.Action = holdem.ActionBet
		}
		request.RaiseTo = target
		return request
	}
	passive := func() holdem.ActionRequest {
		if options.CanCheck {
			request.Action = holdem.ActionCheck
		} else {
			request.Action = holdem.ActionFold
		}
		return request
	}
	call := func() holdem.ActionRequest {
		if options.CanCall {
			request.Action = holdem.ActionCall
		} else if options.CanCheck {
			request.Action = holdem.ActionCheck
		} else {
			request.Action = holdem.ActionAllIn
		}
		return request
	}
	canAggress := options.CanBet || options.CanRaise || options.CanAllIn
	noise := (source.Float64() - 0.5) * 0.16

	if len(snapshot.Board) == 0 {
		strength := preflopStrength(snapshot.HoleCards) + noise
		raised := highest > 20
		switch {
		case !raised && strength >= style.open && canAggress:
			return sized(60 + 20*int64(countLimpers(snapshot)))
		case raised && strength >= style.reraise && canAggress:
			return sized(highest * 3)
		case raised && strength >= style.call:
			return call()
		case !raised && strength >= style.call-0.1-style.loose*0.2:
			return call()
		case options.ToCall <= 10 && strength >= 0.3:
			return call()
		}
		return passive()
	}

	read := readPostflop(snapshot.HoleCards, snapshot.Board)
	strong := read.category >= holdem.TwoPair
	if options.ToCall > 0 {
		switch {
		case strong && source.Float64() < style.aggression && canAggress:
			return sized(highest * 3)
		case strong || read.topPair:
			return call()
		case (read.category == holdem.OnePair || read.draw) && options.ToCall*2 <= pot+options.ToCall:
			return call()
		case source.Float64() < style.loose*0.6:
			return call()
		case source.Float64() < style.bluff*0.3 && canAggress:
			return sized(highest * 3)
		}
		return passive()
	}
	switch {
	case strong && canAggress:
		return sized(pot * 2 / 3)
	case read.topPair && source.Float64() < 0.4+style.aggression*0.4 && canAggress:
		return sized(pot / 2)
	case read.draw && source.Float64() < style.bluff+0.2 && canAggress:
		return sized(pot / 2)
	case source.Float64() < style.bluff && canAggress:
		return sized(pot * 2 / 3)
	}
	return passive()
}

func countLimpers(snapshot Snapshot) int {
	count := 0
	for _, seat := range snapshot.Seats {
		if seat.StreetBet == 20 && seat.LastAction == "call" {
			count++
		}
	}
	return count
}

type recordingModel struct {
	inner review.Model
	mu    sync.Mutex
	calls []map[string]any
}

func (model *recordingModel) Name() string { return model.inner.Name() }

func (model *recordingModel) Complete(ctx context.Context, system, user string) (review.Completion, error) {
	started := time.Now()
	completion, err := model.inner.Complete(ctx, system, user)
	model.mu.Lock()
	defer model.mu.Unlock()
	record := map[string]any{
		"user": user, "content": completion.Content, "model": completion.Model,
		"inputTokens": completion.InputTokens, "outputTokens": completion.OutputTokens,
		"seconds": time.Since(started).Seconds(),
	}
	if err != nil {
		record["error"] = err.Error()
	}
	model.calls = append(model.calls, record)
	return completion, err
}

func readReviewEnv(t *testing.T, path string) map[string]string {
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "REVIEW_") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values
}

func TestLiveReview(t *testing.T) {
	envPath, outDir := os.Getenv("REVIEW_LIVE_ENV"), os.Getenv("REVIEW_LIVE_OUT")
	if envPath == "" || outDir == "" {
		t.Skip("live review is not requested")
	}
	env := readReviewEnv(t, envPath)
	timeout := 300 * time.Second
	client, err := review.NewOpenAIClient(review.OpenAIConfig{
		BaseURL: env["REVIEW_BASE_URL"], Model: env["REVIEW_MODEL"], APIKey: env["REVIEW_API_KEY"],
		Timeout: timeout, JSONMode: env["REVIEW_JSON_MODE"] != "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	seed := int64(20260923)
	source := rand.New(rand.NewSource(seed))
	clock := &replayClock{now: time.Unix(1_800_000_000, 0)}
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	users := []string{"hero", "fish", "maniac", "nit", "regular"}
	for _, userID := range users {
		if _, err := chips.TopUp(ctx, userID, "topup-"+userID, 10_000_000); err != nil {
			t.Fatal(err)
		}
	}
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{Bankroll: chips, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	created, err := rooms.CreateConfigured(ctx, room.Participant{UserID: users[0], DisplayName: "英雄"}, room.CreateOptions{
		Preset: room.PresetStandard, SmallBlind: 10, BigBlind: 20, MaxBuyIn: 4_000, BuyIn: 2_000, RequestID: "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range users[1:] {
		if _, err := rooms.JoinWithBuyIn(ctx, room.Participant{UserID: userID, DisplayName: userID}, room.JoinOptions{
			Code: created.Code, BuyIn: 2_000, RequestID: "join-" + userID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := rooms.UpdateRakeSettings(ctx, created.RoomID, room.RakeSettings{
		Enabled: true, BasisPoints: 500, Cap: 60, PostflopEnabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	store := history.NewInMemoryStore()
	manager, err := NewWithConfig(rooms, churnRandom{source: rand.New(rand.NewSource(seed + 1))}, ManagerConfig{
		Bankroll: chips, History: store, Now: clock.Now,
		RakeRecipient: func(context.Context) (string, error) { return "rake_admin", nil },
		AfterFunc:     func(time.Duration, func()) ScheduledTimer { return &fakeScheduledTimer{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range users {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}

	var handIDs []string
	step := 0
	for played := 0; played < 220; step++ {
		if step > 100_000 {
			t.Fatal("too many steps")
		}
		snapshot, err := manager.Snapshot(ctx, "hero", created.RoomID)
		if err != nil {
			t.Fatal(err)
		}
		for _, seat := range snapshot.Seats {
			if seat.Stack < 400 {
				_, _ = manager.Rebuy(ctx, seat.UserID, created.RoomID, fmt.Sprintf("rebuy-%s-%d", seat.UserID, step), 2_000-seat.Stack)
			}
		}
		for _, userID := range users {
			snapshot, _ = manager.SetReady(ctx, userID, true)
		}
		if snapshot.Phase != holdem.PhasePreflop && snapshot.Phase != holdem.PhaseRunoutChoice {
			continue
		}
		handID := snapshot.HandID
		for snapshot.Phase != holdem.PhaseWaitingNextHand {
			step++
			if snapshot.Phase == holdem.PhaseRunoutChoice && snapshot.RunoutChoice != nil {
				for _, userID := range snapshot.RunoutChoice.EligiblePlayerIDs {
					if _, chosen := snapshot.RunoutChoice.Choices[userID]; !chosen {
						_, _ = manager.SubmitRunoutChoice(ctx, userID, created.RoomID, 1+source.Intn(2))
						break
					}
				}
				snapshot, _ = manager.Snapshot(ctx, "hero", created.RoomID)
				continue
			}
			actor := snapshot.CurrentAction.UserID
			view, err := manager.Snapshot(ctx, actor, created.RoomID)
			if err != nil {
				t.Fatal(err)
			}
			if actor == "hero" && source.Intn(40) == 0 {
				runtime := manager.existingRuntime(created.RoomID)
				for attempt := 0; attempt < 5; attempt++ {
					runtime.mu.Lock()
					revision := runtime.engine.Revision()
					clock.now = runtime.deadline.Add(time.Second)
					generation := runtime.timerGeneration
					runtime.mu.Unlock()
					manager.handleTimeout(created.RoomID, generation)
					runtime.mu.Lock()
					acted := runtime.engine.Revision() != revision
					runtime.mu.Unlock()
					if acted {
						break
					}
				}
			} else {
				var me SeatSnapshot
				for _, seat := range view.Seats {
					if seat.UserID == actor {
						me = seat
					}
				}
				request := liveAction(source, liveStyles[actor], view, me)
				request.ActionID = fmt.Sprintf("act-%d", step)
				if _, _, err := manager.SubmitAction(ctx, actor, created.RoomID, request); err != nil {
					fallback := holdem.ActionRequest{ActionID: fmt.Sprintf("act-%d-f", step), HandID: view.HandID, TableRevision: view.TableRevision}
					if view.CurrentAction.Options.CanCheck {
						fallback.Action = holdem.ActionCheck
					} else {
						fallback.Action = holdem.ActionFold
					}
					if _, _, err := manager.SubmitAction(ctx, actor, created.RoomID, fallback); err != nil {
						t.Fatalf("action %+v then fallback: %v", request, err)
					}
				}
			}
			clock.now = clock.now.Add(5 * time.Second)
			snapshot, err = manager.Snapshot(ctx, "hero", created.RoomID)
			if err != nil {
				t.Fatal(err)
			}
		}
		clock.now = clock.now.Add(20 * time.Second)
		played++
		handIDs = append(handIDs, handID)
	}

	// 挑本人有代表性的几手
	type pick struct{ handID, label string }
	var picks []pick
	chosen := map[string]bool{}
	add := func(label string, match func(history.Hand) bool) {
		for index := len(handIDs) - 1; index >= 0; index-- {
			handID := handIDs[index]
			if chosen[handID] {
				continue
			}
			hand, err := store.HandForPlayer("hero", handID)
			if err != nil || !heroActed(hand) || !match(hand) {
				continue
			}
			chosen[handID] = true
			picks = append(picks, pick{handID, label})
			return
		}
		t.Logf("no hand for %s", label)
	}
	add("showdown_multi_street", func(hand history.Hand) bool {
		return hand.Showdown && len(hand.Board) == 5 && !heroFolded(hand) && streets(hand) >= 3
	})
	add("hero_all_in", func(hand history.Hand) bool { return heroDid(hand, "all_in") })
	add("preflop_3bet", func(hand history.Hand) bool { return preflopRaises(hand) >= 2 && heroDid(hand, "raise") })
	add("river_fold", func(hand history.Hand) bool { return heroFoldedOn(hand, "river") })
	add("multiway_flop", func(hand history.Hand) bool { return flopPlayers(hand) >= 3 })
	add("timed_out", func(hand history.Hand) bool {
		for _, action := range hand.Actions {
			if action.UserID == "hero" && action.TimedOut {
				return true
			}
		}
		return false
	})
	add("run_twice", func(hand history.Hand) bool { return len(hand.RunoutBoards) > 1 })
	add("lost_big_pot", func(hand history.Hand) bool {
		for _, player := range hand.Players {
			if player.UserID == "hero" && player.Delta <= -600 {
				return true
			}
		}
		return false
	})

	var inner review.Model = client
	if os.Getenv("REVIEW_LIVE_DRY") != "" {
		inner = dryModel{}
	}
	recorder := &recordingModel{inner: inner}
	service, err := review.NewService(review.NewMemoryStore(), store, recorder, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetAccess(ctx, "hero", true, "admin"); err != nil {
		t.Fatal(err)
	}
	summary := []map[string]any{}
	for index, value := range picks {
		before := len(recorder.calls)
		if _, err := service.Request(ctx, "hero", value.handID); err != nil {
			t.Logf("%s: request %v", value.label, err)
			continue
		}
		service.ProcessNext(ctx)
		result, err := service.Get(ctx, "hero", value.handID)
		if err != nil {
			t.Fatal(err)
		}
		hand, _ := store.HandForPlayer("hero", value.handID)
		output := map[string]any{
			"label": value.label, "handId": value.handID, "review": result,
			"calls": recorder.calls[before:], "hand": hand,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		name := filepath.Join(outDir, fmt.Sprintf("%02d_%s.json", index+1, value.label))
		if err := os.WriteFile(name, data, 0o644); err != nil {
			t.Fatal(err)
		}
		summary = append(summary, map[string]any{
			"label": value.label, "status": result.Status, "failure": result.Error,
			"inputTokens": result.InputTokens, "outputTokens": result.OutputTokens, "calls": len(recorder.calls) - before,
		})
		t.Logf("%s: status=%s failure=%s tokens=%d/%d", value.label, result.Status, result.Error, result.InputTokens, result.OutputTokens)
	}
	data, _ := json.MarshalIndent(summary, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "summary.json"), data, 0o644)
}

func heroActed(hand history.Hand) bool {
	for _, action := range hand.Actions {
		if action.UserID == "hero" {
			return true
		}
	}
	return false
}

func heroDid(hand history.Hand, kind string) bool {
	for _, action := range hand.Actions {
		if action.UserID == "hero" && action.Type == kind {
			return true
		}
	}
	return false
}

func heroFolded(hand history.Hand) bool { return heroDid(hand, "fold") }

func heroFoldedOn(hand history.Hand, street string) bool {
	for _, action := range hand.Actions {
		if action.UserID == "hero" && action.Type == "fold" && action.Street == street {
			return true
		}
	}
	return false
}

func streets(hand history.Hand) int {
	seen := map[string]bool{}
	for _, action := range hand.Actions {
		if action.UserID == "hero" {
			seen[action.Street] = true
		}
	}
	return len(seen)
}

func preflopRaises(hand history.Hand) int {
	count := 0
	for _, action := range hand.Actions {
		if action.Street == "preflop" && (action.Type == "raise" || action.Type == "all_in") {
			count++
		}
	}
	return count
}

func flopPlayers(hand history.Hand) int {
	players := map[string]bool{}
	for _, action := range hand.Actions {
		if action.Street == "flop" {
			players[action.UserID] = true
		}
	}
	return len(players)
}

type dryModel struct{}

func (dryModel) Name() string { return "dry" }

func (dryModel) Complete(context.Context, string, string) (review.Completion, error) {
	return review.Completion{Content: `{"summary":"dry"}`, Model: "dry"}, nil
}

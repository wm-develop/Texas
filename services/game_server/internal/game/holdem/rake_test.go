package holdem

import (
	"strconv"
	"testing"
)

var rakeActionCounter int

func TestRakeAmount(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		config   RakeConfig
		pot      int64
		flopSeen bool
		want     int64
	}{
		{"关闭时不抽", RakeConfig{BasisPoints: 500, Cap: 100}, 1000, true, 0},
		{"按比例", RakeConfig{Enabled: true, BasisPoints: 500}, 1000, false, 50},
		{"向下取整", RakeConfig{Enabled: true, BasisPoints: 250}, 999, false, 24},
		{"比例部分封顶", RakeConfig{Enabled: true, BasisPoints: 500, Cap: 30}, 1000, false, 30},
		{"上限为 0 表示不封顶", RakeConfig{Enabled: true, BasisPoints: 1000}, 100000, false, 10000},
		{"翻后加抽不受上限限制", RakeConfig{Enabled: true, BasisPoints: 500, Cap: 30, PostflopEnabled: true, PostflopAmount: 20}, 1000, true, 50},
		{"没发翻牌不加抽", RakeConfig{Enabled: true, BasisPoints: 500, Cap: 30, PostflopEnabled: true, PostflopAmount: 20}, 1000, false, 30},
		{"加抽开关关闭", RakeConfig{Enabled: true, BasisPoints: 500, PostflopAmount: 20}, 1000, true, 50},
		{"只开翻后加抽", RakeConfig{Enabled: true, PostflopEnabled: true, PostflopAmount: 20}, 1000, true, 20},
		// 短码在盲注位全下再补发公共牌：底池只有几个筹码，一个大盲的加抽会把它抽光
		{"底池不足两个大盲不加抽", RakeConfig{Enabled: true, BasisPoints: 1000, PostflopEnabled: true, PostflopAmount: 20}, 39, true, 3},
		{"底池恰好两个大盲起加抽", RakeConfig{Enabled: true, BasisPoints: 1000, PostflopEnabled: true, PostflopAmount: 20}, 40, true, 24},
		{"空底池", RakeConfig{Enabled: true, BasisPoints: 500}, 0, true, 0},
		{"接近钱包上限的底池不溢出", RakeConfig{Enabled: true, BasisPoints: 1000}, 9_000_000_000_000_007, false, 900_000_000_000_000},
	} {
		if got := testCase.config.Amount(testCase.pot, testCase.flopSeen, 20); got != testCase.want {
			t.Errorf("%s: got %d want %d", testCase.name, got, testCase.want)
		}
	}
}

func TestRakeConfigValidation(t *testing.T) {
	const bigBlind = 20
	for _, testCase := range []struct {
		name   string
		config RakeConfig
		valid  bool
	}{
		{"零值", RakeConfig{}, true},
		{"比例上限 10%", RakeConfig{BasisPoints: MaximumRakeBasisPoints}, true},
		{"比例超限", RakeConfig{BasisPoints: MaximumRakeBasisPoints + 1}, false},
		{"比例为负", RakeConfig{BasisPoints: -1}, false},
		{"上限为负", RakeConfig{Cap: -1}, false},
		{"加抽等于一个大盲", RakeConfig{PostflopAmount: bigBlind}, true},
		{"加抽超过一个大盲", RakeConfig{PostflopAmount: bigBlind + 1}, false},
		{"加抽为负", RakeConfig{PostflopAmount: -1}, false},
	} {
		if got := testCase.config.Valid(bigBlind); got != testCase.valid {
			t.Errorf("%s: got %v want %v", testCase.name, got, testCase.valid)
		}
	}
}

func rakeTestTable(t *testing.T, config RakeConfig, stacks map[int]int64) *Table {
	t.Helper()
	table, err := NewTable(Config{TableID: "room_rake", MaxSeats: 6, SmallBlind: 10, BigBlind: 20})
	if err != nil {
		t.Fatal(err)
	}
	names := map[int]string{1: "alice", 2: "bob", 3: "carol"}
	for seat := 1; seat <= len(stacks); seat++ {
		if err := table.AddPlayer(names[seat], seat, stacks[seat]); err != nil {
			t.Fatal(err)
		}
		if err := table.SetReady(names[seat], true); err != nil {
			t.Fatal(err)
		}
	}
	if err := table.SetRake(config); err != nil {
		t.Fatal(err)
	}
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatal(err)
	}
	return table
}

func act(t *testing.T, table *Table, action ActionType, raiseTo int64) ActionResult {
	t.Helper()
	rakeActionCounter++
	result, err := table.SubmitAction(ActionRequest{
		ActionID: "act-" + strconv.Itoa(rakeActionCounter),
		PlayerID: table.CurrentPlayerID(), HandID: table.HandID(),
		TableRevision: table.Revision(), Action: action, RaiseTo: raiseTo,
	})
	if err != nil {
		t.Fatalf("%s by %s in %s: %v", action, table.CurrentPlayerID(), table.Phase(), err)
	}
	return result
}

func settlementTotals(settlement Settlement) (awarded int64, deltas int64) {
	for _, award := range settlement.PotAwards {
		awarded += award.Amount
	}
	for _, entry := range settlement.LedgerEntries {
		deltas += entry.Delta
	}
	return awarded, deltas
}

// 翻前全弃：只抽比例部分，未被跟注的大盲差额退回、不进抽水基数。
func TestRakeOnPreflopFoldOutUsesTheContestedPotOnly(t *testing.T) {
	table := rakeTestTable(t, RakeConfig{Enabled: true, BasisPoints: 1000, PostflopEnabled: true, PostflopAmount: 20},
		map[int]int64{1: 1000, 2: 1000, 3: 1000})
	for table.Phase() != PhaseWaitingNextHand {
		act(t, table, ActionFold, 0)
	}
	settlement := table.LastSettlement()
	// 小盲 10 与大盲里被匹配的 10 构成底池 20；大盲多出的 10 没人跟，退回。
	if settlement.RakeBase != 20 || settlement.Rake != 2 {
		t.Fatalf("rake base=%d rake=%d", settlement.RakeBase, settlement.Rake)
	}
	awarded, deltas := settlementTotals(settlement)
	if awarded != settlement.RakeBase-settlement.Rake {
		t.Fatalf("awarded=%d base=%d rake=%d", awarded, settlement.RakeBase, settlement.Rake)
	}
	if deltas+settlement.Rake != 0 {
		t.Fatalf("deltas=%d rake=%d", deltas, settlement.Rake)
	}
}

// 一人全下、其余全弃：没人跟的那部分原样退回，抽水只按真正有争议的底池算，
// 赢家不会因为赢下这手反而亏钱。
func TestRakeIgnoresTheUncalledAllIn(t *testing.T) {
	table := rakeTestTable(t, RakeConfig{Enabled: true, BasisPoints: 500},
		map[int]int64{1: 5000, 2: 5000, 3: 5000})
	shover := table.CurrentPlayerID()
	startStack := int64(5000)
	act(t, table, ActionAllIn, 0)
	for table.Phase() != PhaseWaitingNextHand {
		act(t, table, ActionFold, 0)
	}
	settlement := table.LastSettlement()
	// 小盲 10 + 大盲 20 + 全下里被大盲匹配的 20 = 50
	if settlement.RakeBase != 50 || settlement.Rake != 2 {
		t.Fatalf("rake base=%d rake=%d", settlement.RakeBase, settlement.Rake)
	}
	if got := settlement.StacksByPlayer[shover]; got != startStack+30-2 {
		t.Fatalf("shover stack=%d, want %d", got, startStack+30-2)
	}
}

// 打到摊牌：比例封顶后再加翻后加抽；筹码守恒为「各人输赢之和 + 抽水 = 0」。
func TestRakeAtShowdownAddsThePostflopCharge(t *testing.T) {
	table := rakeTestTable(t, RakeConfig{Enabled: true, BasisPoints: 1000, Cap: 5, PostflopEnabled: true, PostflopAmount: 20},
		map[int]int64{1: 1000, 2: 1000, 3: 1000})
	for table.Phase() != PhaseWaitingNextHand {
		options, _ := table.CurrentActionOptions()
		switch {
		case options.CanCheck:
			act(t, table, ActionCheck, 0)
		default:
			act(t, table, ActionCall, 0)
		}
	}
	settlement := table.LastSettlement()
	if !settlement.Showdown || settlement.RakeBase != 60 || settlement.Rake != 25 {
		t.Fatalf("showdown=%v base=%d rake=%d", settlement.Showdown, settlement.RakeBase, settlement.Rake)
	}
	awarded, deltas := settlementTotals(settlement)
	if awarded != 35 || deltas != -25 {
		t.Fatalf("awarded=%d deltas=%d", awarded, deltas)
	}
	var stacks int64
	for _, stack := range settlement.StacksByPlayer {
		stacks += stack
	}
	if stacks != 3000-25 {
		t.Fatalf("stacks=%d", stacks)
	}
}

// 有边池时抽水从主池起依次扣，扣除之和恒等于总抽水，不逐池各自取整。
func TestRakeWithSidePotsConservesExactly(t *testing.T) {
	table := rakeTestTable(t, RakeConfig{Enabled: true, BasisPoints: 333},
		map[int]int64{1: 137, 2: 1000, 3: 1000})
	for table.Phase() != PhaseWaitingNextHand {
		options, _ := table.CurrentActionOptions()
		switch {
		case table.Phase() == PhasePreflop && options.CanAllIn && table.CurrentPlayerID() == "alice":
			act(t, table, ActionAllIn, 0)
		case options.CanCheck:
			act(t, table, ActionCheck, 0)
		case options.CanCall:
			act(t, table, ActionCall, 0)
		default:
			act(t, table, ActionAllIn, 0)
		}
	}
	settlement := table.LastSettlement()
	want := settlement.RakeBase * 333 / 10000
	if settlement.Rake != want || want == 0 {
		t.Fatalf("base=%d rake=%d want=%d", settlement.RakeBase, settlement.Rake, want)
	}
	awarded, deltas := settlementTotals(settlement)
	if awarded+settlement.Rake != settlement.RakeBase || deltas+settlement.Rake != 0 {
		t.Fatalf("awarded=%d rake=%d base=%d deltas=%d", awarded, settlement.Rake, settlement.RakeBase, deltas)
	}
}

// 规则只能在两手之间改，且跨崩溃恢复保留。
func TestRakeSettingsAreHandScopedAndSurviveRestore(t *testing.T) {
	config := RakeConfig{Enabled: true, BasisPoints: 500, Cap: 100, PostflopEnabled: true, PostflopAmount: 20}
	table := rakeTestTable(t, config, map[int]int64{1: 1000, 2: 1000, 3: 1000})
	if err := table.SetRake(RakeConfig{}); err == nil {
		t.Fatal("rake settings must not change while a hand is running")
	}
	restored, err := RestoreTable(table.State())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Rake() != config {
		t.Fatalf("restored rake=%#v", restored.Rake())
	}
	state := table.State()
	state.Rake.PostflopAmount = 21
	if _, err := RestoreTable(state); err == nil {
		t.Fatal("an out-of-range rake must not be restored")
	}
}

// 不开抽水时一切照旧。
func TestNoRakeByDefault(t *testing.T) {
	table := rakeTestTable(t, RakeConfig{}, map[int]int64{1: 1000, 2: 1000, 3: 1000})
	for table.Phase() != PhaseWaitingNextHand {
		act(t, table, ActionFold, 0)
	}
	settlement := table.LastSettlement()
	_, deltas := settlementTotals(settlement)
	if settlement.Rake != 0 || deltas != 0 {
		t.Fatalf("rake=%d deltas=%d", settlement.Rake, deltas)
	}
}

// 抽水按各池大小摊开，零头逐枚补给小数部分最大的池；各池之和恰为总抽水，
// 哪个池都不会被扣光。
func TestSplitRakeIsProportional(t *testing.T) {
	for _, testCase := range []struct {
		name string
		rake int64
		pots []int64
		want []int64
	}{
		{"单池", 50, []int64{1000}, []int64{50}},
		{"主池小边池大", 502, []int64{150, 9900}, []int64{7, 495}},
		{"零头补给小数部分最大的池", 10, []int64{100, 100, 100}, []int64{4, 3, 3}},
		{"不抽", 0, []int64{300, 600}, []int64{0, 0}},
		{"抽走全部", 900, []int64{300, 600}, []int64{300, 600}},
		// 建房不限盲注与带入：两个 int64 直接相乘在这种底池里会溢出，算出负的份额，
		// 而各池之和仍等于总抽水，守恒校验发现不了
		{"天文数字的底池不溢出", 3_600_000_000, []int64{6_000_000_000, 30_000_000_000}, []int64{600_000_000, 3_000_000_000}},
		{"万亿级底池立刻算完", 600_000_000_001, []int64{3_000_000_000_000, 3_000_000_000_000}, []int64{300_000_000_001, 300_000_000_000}},
	} {
		pots := make([]Pot, len(testCase.pots))
		for index, amount := range testCase.pots {
			pots[index] = Pot{Amount: amount}
		}
		shares, err := splitRake(testCase.rake, pots)
		if err != nil {
			t.Fatalf("%s: %v", testCase.name, err)
		}
		var total int64
		for index, share := range shares {
			total += share
			if share != testCase.want[index] || share > testCase.pots[index] {
				t.Errorf("%s: shares=%v want %v", testCase.name, shares, testCase.want)
				break
			}
		}
		if total != testCase.rake {
			t.Errorf("%s: shares sum to %d, rake is %d", testCase.name, total, testCase.rake)
		}
	}
	if _, err := splitRake(11, []Pot{{Amount: 10}}); err == nil {
		t.Fatal("a rake above the pot must be rejected")
	}
}

// 短码全下的人只能争主池：抽水按池的大小分摊，他只付主池那一份，
// 而不是一个人替边池付掉全部抽水、牌最大却拿不到钱。
func TestShortAllInWinnerOnlyPaysTheMainPotShare(t *testing.T) {
	table := rakeTestTable(t, RakeConfig{Enabled: true, BasisPoints: 1000},
		map[int]int64{1: 60, 2: 2000, 3: 2000})
	for table.Phase() != PhaseWaitingNextHand {
		options, _ := table.CurrentActionOptions()
		switch {
		case table.CurrentPlayerID() == "alice":
			act(t, table, ActionAllIn, 0)
		case table.Phase() == PhasePreflop && options.CanAllIn:
			act(t, table, ActionAllIn, 0)
		case options.CanCheck:
			act(t, table, ActionCheck, 0)
		default:
			act(t, table, ActionCall, 0)
		}
	}
	settlement := table.LastSettlement()
	// 主池 180、边池 3880，合计 4060，10% 抽 406：主池只摊到 18
	if settlement.RakeBase != 4060 || settlement.Rake != 406 {
		t.Fatalf("base=%d rake=%d", settlement.RakeBase, settlement.Rake)
	}
	var mainPot int64
	for _, award := range settlement.PotAwards {
		if award.PotIndex == 0 {
			mainPot += award.Amount
		}
	}
	if mainPot != 162 {
		t.Fatalf("main pot after rake=%d, want 162 (180 minus its own 10%%): %#v", mainPot, settlement.PotAwards)
	}
	awarded, deltas := settlementTotals(settlement)
	if awarded+settlement.Rake != settlement.RakeBase || deltas+settlement.Rake != 0 {
		t.Fatalf("awarded=%d rake=%d base=%d deltas=%d", awarded, settlement.Rake, settlement.RakeBase, deltas)
	}
}

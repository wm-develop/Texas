package review

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Result 是模型给出的复盘，按固定结构返回，客户端原生渲染。
type Result struct {
	// Situation 是这一手的基本局面（本人位置、人数、盲注、各家码量），由服务端
	// 按交给模型的同一份数据填写，不经过模型：玩家据此能核对 AI 看到的桌面。
	Situation *Situation `json:"situation,omitempty"`
	// Summary 是对本人这一手整体打法的总评。
	Summary string `json:"summary"`
	// Decisions 逐条点评本人的每个决策，Step 对应回放时间轴的步号。
	Decisions []Decision `json:"decisions"`
	// KeyLessons 是这一手最值得记住的几点。
	KeyLessons []string `json:"keyLessons"`
	// OpponentNotes 是结合对手打法倾向的判断。
	OpponentNotes []string `json:"opponentNotes"`
	// Hindsight 是结合摊牌亮出的底牌做的结果回顾，与「决策当时」的评价分开。
	Hindsight string `json:"hindsight"`
}

// Situation 是一手牌的基本局面。
type Situation struct {
	HeroPosition string      `json:"heroPosition"`
	HoleCards    []string    `json:"holeCards"`
	Players      int         `json:"players"`
	SmallBlind   int64       `json:"smallBlind"`
	BigBlind     int64       `json:"bigBlind"`
	Seats        []SeatFacts `json:"seats"`
}

// Decision 是对本人一个决策的点评。
type Decision struct {
	Step      int    `json:"step"`
	Verdict   string `json:"verdict"`
	Reasoning string `json:"reasoning"`
	// BestAction 是这个决策点的最佳行动；本次行动已经最佳时也要写明。
	BestAction string `json:"bestAction"`
	// 以下三项是模型的估算：对手在这条行动线上的范围胜率，本次行动与最佳行动
	// 从这个决策点起的期望收益（以大盲计，弃牌为 0）。拿不准时不给。
	EquityVsRange *float64 `json:"equityVsRangePercent,omitempty"`
	EVTaken       *float64 `json:"evTakenBB,omitempty"`
	EVBest        *float64 `json:"evBestBB,omitempty"`
	// Facts 是服务端算好的这一步的精确数字（底池、需跟注、赔率、牌力、对随机手牌
	// 胜率等），不经过模型。
	Facts *DecisionFacts `json:"facts,omitempty"`
}

// modelDecision 是模型输出的一条点评。数字有时写成字符串，按宽松规则解析；
// 旧写法的 betterOption 也认。
type modelDecision struct {
	Step          int       `json:"step"`
	Verdict       string    `json:"verdict"`
	Reasoning     string    `json:"reasoning"`
	BestAction    string    `json:"bestAction"`
	BetterOption  string    `json:"betterOption"`
	EquityVsRange flexFloat `json:"equityVsRangePercent"`
	EVTaken       flexFloat `json:"evTakenBB"`
	EVBest        flexFloat `json:"evBestBB"`
}

// flexFloat 接受数字、数字字符串（可带 % 或 BB）和 null。
type flexFloat struct{ value *float64 }

func (number *flexFloat) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" || text == "" {
		return nil
	}
	if unquoted, err := strconv.Unquote(text); err == nil {
		text = unquoted
	}
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(text), "%"), "BB"))
	text = strings.TrimSuffix(strings.TrimSpace(text), "bb")
	parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil // 写不成数就当没给，不让整条点评作废
	}
	number.value = &parsed
	return nil
}

// 估算值的合理范围：超出就是模型写错了单位，不显示。
const maximumEVBigBlinds = 5000

// 点评的四档结论，以及模型常用的同义说法。模型不按要求写时宁可认出来，也不要
// 把一个明确的「失误」显示成「有争议」。
var verdicts = map[string]string{
	"好": "好", "很好": "好", "很棒": "好", "优秀": "好", "正确": "好", "最优": "好", "不错": "好", "good": "好",
	"合理": "合理", "可以": "合理", "尚可": "合理", "可接受": "合理", "标准": "合理", "reasonable": "合理", "ok": "合理",
	"有争议": "有争议", "争议": "有争议", "可商榷": "有争议", "边缘": "有争议", "questionable": "有争议",
	"失误": "失误", "错误": "失误", "错误的": "失误", "严重失误": "失误", "大错": "失误", "不合理": "失误",
	"不好": "失误", "较差": "失误", "mistake": "失误", "error": "失误", "bad": "失误", "wrong": "失误",
}

// 单段文字的上限：防止模型失控输出拖垮客户端，也免得一条结果占掉太多存储。
const (
	maximumTextRunes = 1500
	maximumListItems = 12
	maximumItemRunes = 400
	maximumDecisions = 40
)

var errInvalidResult = errors.New("model output is not a valid review")

// 模型有时给结论加上引号、书名号或括号。
const verdictPunctuation = "「」『』“”‘’【】（）()[]<>《》\"'。.!！ "

// parseResult 从模型的回复里取出复盘。模型偶尔会在 JSON 外面包一层 ```json
// 代码块或多写几句话，这里取第一个完整的 JSON 对象；决策只保留确实是本人行动
// 的那些步。结论按四档归一（认得常见同义说法）；**没写结论、写了认不出的结论**，
// 或本人有决策却一条点评都没有，都算输出不合格（会重问一次）：实测 DeepSeek 有时
// 把输入字段抄回来、漏掉 verdict，若默认成「有争议」，明确的失误也会被显示成争议。
func parseResult(content string, viewerSteps map[int]bool) (*Result, error) {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return nil, errInvalidResult
	}
	var raw struct {
		Summary       string          `json:"summary"`
		Decisions     []modelDecision `json:"decisions"`
		KeyLessons    []string        `json:"keyLessons"`
		OpponentNotes []string        `json:"opponentNotes"`
		Hindsight     string          `json:"hindsight"`
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &raw); err != nil {
		return nil, errInvalidResult
	}
	result := Result{
		Summary: raw.Summary, KeyLessons: raw.KeyLessons, OpponentNotes: raw.OpponentNotes, Hindsight: raw.Hindsight,
	}
	result.Summary = clip(strings.TrimSpace(result.Summary), maximumTextRunes)
	result.Hindsight = clip(strings.TrimSpace(result.Hindsight), maximumTextRunes)
	if result.Summary == "" {
		return nil, errInvalidResult
	}
	seen := make(map[int]bool, len(raw.Decisions))
	decisions := make([]Decision, 0, len(raw.Decisions))
	for _, item := range raw.Decisions {
		if !viewerSteps[item.Step] || seen[item.Step] || len(decisions) >= maximumDecisions {
			continue
		}
		seen[item.Step] = true
		verdict, ok := verdicts[strings.ToLower(strings.Trim(strings.TrimSpace(item.Verdict), verdictPunctuation))]
		if !ok {
			return nil, errInvalidResult
		}
		decision := Decision{
			Step: item.Step, Verdict: verdict,
			Reasoning:  clip(strings.TrimSpace(item.Reasoning), maximumTextRunes),
			BestAction: clip(strings.TrimSpace(item.BestAction), maximumItemRunes),
		}
		if decision.BestAction == "" {
			decision.BestAction = clip(strings.TrimSpace(item.BetterOption), maximumItemRunes)
		}
		if decision.Reasoning == "" {
			continue
		}
		if decision.BestAction == "" {
			// 每一步都要给出最佳行动；本次已经最佳时模型常常省略，替它写明
			if verdict != "好" {
				return nil, errInvalidResult
			}
			decision.BestAction = "本次行动已是最佳选择"
		}
		if value := item.EquityVsRange.value; value != nil && *value >= 0 && *value <= 100 {
			equity := math.Round(*value*10) / 10
			decision.EquityVsRange = &equity
		}
		taken, best := item.EVTaken.value, item.EVBest.value
		// 两个 EV 要成对、在合理范围里，最佳行动的收益不低于本次行动，也不低于 0
		// （弃牌或过牌永远是 0，最佳不可能更差）；自相矛盾的宁可不显示
		if taken != nil && best != nil && math.Abs(*taken) <= maximumEVBigBlinds &&
			math.Abs(*best) <= maximumEVBigBlinds && *best+0.05 >= *taken && *best > -0.05 {
			takenValue, bestValue := math.Round(*taken*100)/100, math.Round(*best*100)/100
			if bestValue < takenValue {
				bestValue = takenValue
			}
			decision.EVTaken, decision.EVBest = &takenValue, &bestValue
		}
		decisions = append(decisions, decision)
	}
	if len(viewerSteps) > 0 && len(decisions) == 0 {
		return nil, errInvalidResult
	}
	result.Decisions = decisions
	result.KeyLessons = clipList(result.KeyLessons)
	result.OpponentNotes = clipList(result.OpponentNotes)
	return &result, nil
}

func clipList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = clip(strings.TrimSpace(value), maximumItemRunes)
		if value == "" {
			continue
		}
		result = append(result, value)
		if len(result) == maximumListItems {
			break
		}
	}
	return result
}

// clip 按字符截断，并去掉数据库存不进的内容：非法 UTF-8 与 NUL（Postgres 的
// text 与 jsonb 都会拒收，整条结果因此存不下来）。
func clip(value string, limit int) string {
	value = strings.ReplaceAll(strings.ToValidUTF8(value, ""), "\x00", "")
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

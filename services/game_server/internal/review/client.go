package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Completion 是一次模型调用的结果。
type Completion struct {
	Content      string
	Model        string
	InputTokens  int64
	OutputTokens int64
	// Truncated 表示输出到了长度上限被截断（finish_reason 为 length）。推理模型的
	// 上限包含思考过程，REVIEW_MAX_TOKENS 设小了会整段被截掉。
	Truncated bool
}

// Model 是大模型的抽象：输入系统提示与用户消息，返回文本。测试用假实现。
type Model interface {
	Complete(ctx context.Context, system, user string) (Completion, error)
	Name() string
}

// OpenAIConfig 配置一个兼容 OpenAI Chat Completions 接口的服务（DeepSeek、通义
// 千问的兼容模式等）。
type OpenAIConfig struct {
	BaseURL string
	Model   string
	APIKey  string
	Timeout time.Duration
	// JSONMode 为真时请求 response_format=json_object；服务端不支持而报 400 时
	// 自动去掉它重试一次。
	JSONMode bool
	// MaxTokens 为 0 时不设上限，由服务端默认值决定。
	MaxTokens int
	// Thinking 是思考模式开关：enabled、disabled，空串表示不发这个字段（有的兼容
	// 服务不认识它）。
	Thinking string
	// ReasoningEffort 是思考强度，原样作为 reasoning_effort 发出；空串不发。
	// DeepSeek 把 xhigh 映射到 high，把 max 当作最高档。
	ReasoningEffort string
	// SendUserID 为真时附带 user_id：由账号推出的匿名编号，DeepSeek 用它做
	// 内容安全与 KVCache 的按用户隔离。不认识这个字段的服务会报参数错误，所以
	// 只在 DeepSeek 上默认打开。
	SendUserID bool
}

type OpenAIClient struct {
	config OpenAIConfig
	http   *http.Client
}

func NewOpenAIClient(config OpenAIConfig) (*OpenAIClient, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if config.BaseURL == "" || strings.TrimSpace(config.Model) == "" || strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("review model requires a base URL, a model name and an API key")
	}
	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Minute
	}
	return &OpenAIClient{config: config, http: &http.Client{Timeout: config.Timeout}}, nil
}

func (client *OpenAIClient) Name() string { return client.config.Model }

// ModelError 是模型服务返回的错误。Detail 已截断，且不含请求里的密钥。
type ModelError struct {
	Status int
	Detail string
}

func (err ModelError) Error() string {
	return fmt.Sprintf("model service returned %d: %s", err.Status, err.Detail)
}

// 模型调用失败的三类处理：
//   - 临时（限流、服务繁忙、超时、网络）：稍后自动重试；
//   - 全局（余额不足、密钥失效）：这一条和排着的都记失败，暂停接收新请求；
//   - 其余（请求格式、参数错误等）：记失败并写日志，重试也不会好。
const (
	failureModelBusy         = "model_busy"
	failureModelTimeout      = "model_timeout"
	failureModelBalance      = "model_insufficient_balance"
	failureModelUnauthorized = "model_unauthorized"
	failureModelError        = "model_error"
)

// classifyModelFailure 把一次调用错误归到上面的原因码。
func classifyModelFailure(err error) string {
	var modelError ModelError
	if errors.As(err, &modelError) {
		switch modelError.Status {
		case http.StatusPaymentRequired:
			return failureModelBalance
		case http.StatusUnauthorized:
			// 403 不算：兼容服务与中转常用它表示地区限制、没有这个模型的权限等，
			// 不是整个账户不能用，不该让整个队列失败
			return failureModelUnauthorized
		case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return failureModelBusy
		}
		return failureModelError
	}
	// 超时单独算：思考模式下一次生成可能就超过时限，重试多半还会超时
	var timeout interface{ Timeout() bool }
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return failureModelTimeout
	}
	// 没拿到完整回应：连接被断开、网络不通、服务资源不足中断，都当临时故障
	var transport transportError
	if errors.As(err, &transport) {
		return failureModelBusy
	}
	return failureModelError
}

// transportError 标记「请求没有拿到模型服务的回应」这类错误。
type transportError struct{ err error }

func (err transportError) Error() string { return err.err.Error() }
func (err transportError) Unwrap() error { return err.err }

type endUserKey struct{}

// WithEndUser 把发起复盘的玩家带到模型调用里，用来生成匿名 user_id。
func WithEndUser(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, endUserKey{}, userID)
}

// anonymousUserID 由账号推出一个稳定的匿名编号：同一个人每次相同，但从编号
// 反推不出账号，符合 DeepSeek「不要包含个人信息」的要求。
func anonymousUserID(userID string) string {
	sum := sha256.Sum256([]byte("texas-review-user:" + userID))
	return "u_" + hex.EncodeToString(sum[:12])
}

func (client *OpenAIClient) Complete(ctx context.Context, system, user string) (Completion, error) {
	completion, err := client.complete(ctx, system, user, client.config.JSONMode)
	var modelError ModelError
	if client.config.JSONMode && errors.As(err, &modelError) && modelError.Status == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(modelError.Detail), "response_format") {
		return client.complete(ctx, system, user, false)
	}
	return completion, err
}

func (client *OpenAIClient) complete(ctx context.Context, system, user string, jsonMode bool) (Completion, error) {
	body := map[string]any{
		"model": client.config.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"stream": false,
	}
	if jsonMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	if client.config.MaxTokens > 0 {
		body["max_tokens"] = client.config.MaxTokens
	}
	if client.config.Thinking != "" {
		body["thinking"] = map[string]string{"type": client.config.Thinking}
	}
	if client.config.ReasoningEffort != "" {
		body["reasoning_effort"] = client.config.ReasoningEffort
	}
	if endUser, ok := ctx.Value(endUserKey{}).(string); client.config.SendUserID && ok && endUser != "" {
		body["user_id"] = anonymousUserID(endUser)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Completion{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.config.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Completion{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.config.APIKey)
	response, err := client.http.Do(request)
	if err != nil {
		return Completion{}, transportError{fmt.Errorf("call model service: %w", err)}
	}
	defer response.Body.Close()
	// 排队等推理时 DeepSeek 会先发空行保持连接，JSON 解析会跳过这些空白
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return Completion{}, transportError{fmt.Errorf("read model response: %w", err)}
	}
	if response.StatusCode/100 != 2 {
		// 按字符截断：中转服务常返回中文报错，按字节截会切出半个汉字，数据库拒收
		detail := clip(strings.TrimSpace(string(data)), 300)
		return Completion{}, ModelError{Status: response.StatusCode, Detail: detail}
	}
	var decoded struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	// DeepSeek 排队时先发空行保持连接，超过 10 分钟还没开始推理就断开：这时拿到的
	// 是 200 加一段空白，按临时故障处理
	if strings.TrimSpace(string(data)) == "" {
		return Completion{}, transportError{errors.New("model service closed the connection before answering")}
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Completion{}, fmt.Errorf("decode model response: %w", err)
	}
	completion := Completion{
		Model: decoded.Model, InputTokens: decoded.Usage.PromptTokens, OutputTokens: decoded.Usage.CompletionTokens,
	}
	if completion.Model == "" {
		completion.Model = client.config.Model
	}
	if len(decoded.Choices) > 0 {
		switch decoded.Choices[0].FinishReason {
		case "length":
			completion.Truncated = true
			completion.Content = decoded.Choices[0].Message.Content
			return completion, nil
		case "insufficient_system_resource", "aborted":
			// 服务端资源不足中途停止：内容不完整，稍后重试
			return completion, transportError{fmt.Errorf("model stopped early: %s", decoded.Choices[0].FinishReason)}
		}
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return completion, errors.New("model returned no content")
	}
	completion.Content = decoded.Choices[0].Message.Content
	return completion, nil
}

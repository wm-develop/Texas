package review

import (
	"bytes"
	"context"
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
// 千问的兼容模式等）。换服务只改这里的三项。
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
		return Completion{}, fmt.Errorf("call model service: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return Completion{}, fmt.Errorf("read model response: %w", err)
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
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Completion{}, fmt.Errorf("decode model response: %w", err)
	}
	completion := Completion{
		Model: decoded.Model, InputTokens: decoded.Usage.PromptTokens, OutputTokens: decoded.Usage.CompletionTokens,
	}
	if completion.Model == "" {
		completion.Model = client.config.Model
	}
	if len(decoded.Choices) > 0 && decoded.Choices[0].FinishReason == "length" {
		completion.Truncated = true
		completion.Content = decoded.Choices[0].Message.Content
		return completion, nil
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return completion, errors.New("model returned no content")
	}
	completion.Content = decoded.Choices[0].Message.Content
	return completion, nil
}

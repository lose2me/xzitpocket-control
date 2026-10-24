package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// ModelOptions configures the OpenAI-compatible vision endpoint.
type ModelOptions struct {
	APIKey  string
	BaseURL string
	Name    string
}

// Model talks to the DeepSeek-compatible chat completions API.
type Model struct {
	client *openai.Client
	name   string
}

// Message is a stored, text-only conversation turn. Images are not persisted:
// they are rebuilt from the PDF for the page range currently being analysed.
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// PromptMessage is the text representation of one message sent to the model.
// Images are recorded as [图片] markers; their base64 bytes are not persisted
// because they can be rebuilt from the source PDF.
type PromptMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// PromptCall is one API request made for a task.
type PromptCall struct {
	Kind      string          `json:"kind"`
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []PromptMessage `json:"messages"`
}

func promptCall(kind, model string, maxTokens int, messages []openai.ChatCompletionMessage) PromptCall {
	call := PromptCall{Kind: kind, Model: model, MaxTokens: maxTokens, Messages: make([]PromptMessage, 0, len(messages))}
	for _, message := range messages {
		var text strings.Builder
		if len(message.MultiContent) > 0 {
			for _, part := range message.MultiContent {
				switch part.Type {
				case openai.ChatMessagePartTypeText:
					if text.Len() > 0 {
						text.WriteString("\n")
					}
					text.WriteString(part.Text)
				case openai.ChatMessagePartTypeImageURL:
					text.WriteString("[图片]")
				}
			}
		} else {
			text.WriteString(message.Content)
		}
		call.Messages = append(call.Messages, PromptMessage{Role: message.Role, Content: text.String()})
	}
	return call
}

// AppendPromptCalls appends new calls to the JSON array stored on a task.
func AppendPromptCalls(existing string, calls []PromptCall) string {
	if len(calls) == 0 {
		return existing
	}
	all := make([]PromptCall, 0)
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &all); err != nil {
			all = nil
		}
	}
	all = append(all, calls...)
	raw, err := json.Marshal(all)
	if err != nil {
		return existing
	}
	return string(raw)
}

// Verdict is the precheck answer.
type Verdict struct {
	Solvable      bool   `json:"solvable"`
	Reason        string `json:"reason"`
	Subject       string `json:"subject"`
	QuestionCount int    `json:"question_count"`
}

// NewModel builds the LLM client. The API key stays in memory only.
func NewModel(opts ModelOptions) (*Model, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("CONTROL_AI_API_KEY 未配置")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "deepseek-flash"
	}
	cfg := openai.DefaultConfig(opts.APIKey)
	cfg.BaseURL = baseURL
	return &Model{client: openai.NewClientWithConfig(cfg), name: name}, nil
}

// Name is the model id recorded on the task row.
func (m *Model) Name() string { return m.name }

// IsPermanentError reports whether the API rejected the request in a way that
// retrying the same payload cannot fix (bad key, unsupported image, ...).
func IsPermanentError(err error) bool {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.HTTPStatusCode >= 400 && apiErr.HTTPStatusCode < 500 && apiErr.HTTPStatusCode != 429
	}
	return false
}

// Precheck asks the model whether the extracted pages form a solvable exam.
func (m *Model) Precheck(ctx context.Context, parts []openai.ChatMessagePart) (Verdict, string, []PromptCall, error) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: precheckSystemPrompt},
		{Role: openai.ChatMessageRoleUser, MultiContent: parts},
	}
	call := promptCall("precheck", m.name, 4096, messages)
	resp, err := m.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:     m.name,
		Messages:  messages,
		MaxTokens: 4096,
	})
	if err != nil {
		return Verdict{}, "", []PromptCall{call}, err
	}
	if len(resp.Choices) == 0 {
		return Verdict{}, "", []PromptCall{call}, fmt.Errorf("AI 未返回内容")
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	verdict, err := parseVerdict(content)
	if err != nil {
		return Verdict{}, content, []PromptCall{call}, err
	}
	return verdict, content, []PromptCall{call}, nil
}

func parseVerdict(content string) (Verdict, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		if index := strings.Index(content, "\n"); index >= 0 {
			content = content[index+1:]
		}
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return Verdict{}, fmt.Errorf("预判结果不是 JSON")
	}
	var verdict Verdict
	if err := json.Unmarshal([]byte(content[start:end+1]), &verdict); err != nil {
		return Verdict{}, fmt.Errorf("解析预判 JSON 失败: %w", err)
	}
	return verdict, nil
}

// Analyze sends one page range in the paper's conversation and returns the
// markdown answer. Long answers are continued in the same conversation.
func (m *Model) Analyze(ctx context.Context, history []Message, parts []openai.ChatMessagePart) (string, []PromptCall, error) {
	messages := make([]openai.ChatCompletionMessage, 0, len(history)+2)
	messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: analysisSystemPrompt})
	for _, message := range history {
		role := message.Role
		if role != openai.ChatMessageRoleAssistant {
			role = openai.ChatMessageRoleUser
		}
		messages = append(messages, openai.ChatCompletionMessage{Role: role, Content: message.Text})
	}
	messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, MultiContent: parts})

	calls := make([]PromptCall, 0, 4)
	var output strings.Builder
	for attempt := 0; attempt < 4; attempt++ {
		kind := "analyze"
		if attempt > 0 {
			kind = "continue"
		}
		calls = append(calls, promptCall(kind, m.name, 32000, messages))
		resp, err := m.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:     m.name,
			Messages:  messages,
			MaxTokens: 32000,
		})
		if err != nil {
			return "", calls, err
		}
		if len(resp.Choices) == 0 {
			return "", calls, fmt.Errorf("AI 未返回内容")
		}
		choice := resp.Choices[0]
		content := strings.TrimSpace(choice.Message.Content)
		output.WriteString(content)
		if choice.FinishReason != "length" {
			return strings.TrimSpace(output.String()), calls, nil
		}
		// The response was cut off; keep going in the same conversation.
		messages = append(messages,
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: choice.Message.Content},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "继续输出，从上次中断的位置接着写，不要重复已经输出的内容。"},
		)
	}
	return strings.TrimSpace(output.String()), calls, nil
}

// BuildContent renders a page range as multimodal parts and as the plain text
// stored in the conversation history.
func BuildContent(paper *Paper, from, to int, instruction string) ([]openai.ChatMessagePart, string, error) {
	if from < 1 {
		from = 1
	}
	if to > paper.TotalPages {
		to = paper.TotalPages
	}
	if from > to {
		return nil, "", fmt.Errorf("页码范围无效")
	}
	parts := []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: instruction}}
	var plain strings.Builder
	plain.WriteString(instruction)
	for pageNumber := from; pageNumber <= to; pageNumber++ {
		page := paper.Pages[pageNumber-1]
		header := fmt.Sprintf("\n\n===== 第 %d 页 =====\n", page.Number)
		parts = append(parts, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: header})
		plain.WriteString(header)
		for _, part := range page.Parts {
			if part.Text != "" {
				parts = append(parts, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: part.Text})
				plain.WriteString(part.Text)
			}
			if part.ImageID == 0 {
				continue
			}
			asset, ok := paper.Images[part.ImageID]
			if !ok {
				continue
			}
			label := fmt.Sprintf("\n[图 %d]\n", part.ImageID)
			parts = append(parts, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: label})
			plain.WriteString(label)
			imagePart, err := imageContentPart(asset)
			if err != nil {
				parts = append(parts, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: "（图片读取失败）"})
				continue
			}
			parts = append(parts, imagePart)
		}
	}
	return parts, strings.TrimSpace(plain.String()), nil
}

func imageContentPart(asset ImageAsset) (openai.ChatMessagePart, error) {
	raw, err := os.ReadFile(asset.Path)
	if err != nil {
		return openai.ChatMessagePart{}, err
	}
	mime := asset.MIME
	if mime == "" {
		mime = "image/webp"
	}
	url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
	return openai.ChatMessagePart{
		Type: openai.ChatMessagePartTypeImageURL,
		ImageURL: &openai.ChatMessageImageURL{
			URL:    url,
			Detail: openai.ImageURLDetailHigh,
		},
	}, nil
}

// EncodeMessages serialises the text-only conversation.
func EncodeMessages(messages []Message) string {
	if len(messages) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// DecodeMessages restores the text-only conversation.
func DecodeMessages(raw string) []Message {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var messages []Message
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return nil
	}
	return messages
}

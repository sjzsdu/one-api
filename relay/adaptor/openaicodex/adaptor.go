package openaicodex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/openaioauth"
	"github.com/songquanpeng/one-api/common/render"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/adaptor"
	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	"github.com/songquanpeng/one-api/relay/meta"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

const defaultCodexInstructions = "You are Codex, a coding assistant."

var fallbackCodexModels = []string{
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.4-nano",
}

type Adaptor struct{}

type codexRequest struct {
	Model             string       `json:"model"`
	Input             []codexInput `json:"input"`
	Instructions      string       `json:"instructions,omitempty"`
	Store             bool         `json:"store"`
	Stream            bool         `json:"stream"`
	MaxOutputTokens   int          `json:"max_output_tokens,omitempty"`
	Temperature       *float64     `json:"temperature,omitempty"`
	TopP              *float64     `json:"top_p,omitempty"`
	Tools             []codexTool  `json:"tools,omitempty"`
	ToolChoice        any          `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool        `json:"parallel_tool_calls,omitempty"`
	Reasoning         *reasoning   `json:"reasoning,omitempty"`
}

type reasoning struct {
	Effort string `json:"effort,omitempty"`
}

type codexInput struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   any    `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type codexTool struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
	Strict      bool   `json:"strict"`
}

type codexEvent struct {
	Type     string        `json:"type"`
	Delta    string        `json:"delta"`
	Text     string        `json:"text"`
	Refusal  string        `json:"refusal"`
	Part     codexPart     `json:"part"`
	Item     codexItem     `json:"item"`
	Response codexResponse `json:"response"`
}

type codexPart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type codexItem struct {
	Type      string         `json:"type"`
	Content   []codexContent `json:"content"`
	CallID    string         `json:"call_id"`
	Name      string         `json:"name"`
	Arguments string         `json:"arguments"`
}

type codexContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type codexResponse struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Output []codexItem    `json:"output"`
	Usage  codexUsage     `json:"usage"`
	Error  *codexAPIError `json:"error"`
}

type codexUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type codexAPIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type codexAggregate struct {
	ID               string
	Content          strings.Builder
	ReasoningContent strings.Builder
	FinishReason     string
	Usage            *relaymodel.Usage
	ToolCalls        []relaymodel.Tool
	Err              *codexAPIError
}

type chatStreamResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []chatStreamChoice `json:"choices"`
	Usage   *relaymodel.Usage  `json:"usage,omitempty"`
}

type chatStreamChoice struct {
	Index        int                `json:"index"`
	Delta        relaymodel.Message `json:"delta"`
	FinishReason *string            `json:"finish_reason,omitempty"`
}

type chatResponse struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []chatResponseChoice `json:"choices"`
	Usage   relaymodel.Usage     `json:"usage"`
}

type chatResponseChoice struct {
	Index        int                `json:"index"`
	Message      relaymodel.Message `json:"message"`
	FinishReason string             `json:"finish_reason"`
}

func (a *Adaptor) Init(_ *meta.Meta) {}

func (a *Adaptor) GetRequestURL(meta *meta.Meta) (string, error) {
	if meta.Mode != relaymode.ChatCompletions {
		return "", fmt.Errorf("OpenAI OAuth Codex channel only supports chat completions")
	}
	return strings.TrimRight(meta.BaseURL, "/") + "/responses", nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *meta.Meta) error {
	adaptor.SetupCommonRequestHeader(c, req, meta)
	cred, err := resolveCredential(meta)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	if cred.AccountID != "" {
		req.Header.Set("Chatgpt-Account-Id", cred.AccountID)
	}
	return nil
}

func (a *Adaptor) ConvertRequest(_ *gin.Context, relayMode int, request *relaymodel.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if relayMode != relaymode.ChatCompletions {
		return nil, fmt.Errorf("OpenAI OAuth Codex channel only supports chat completions")
	}
	input, instructions := translateMessages(request.Messages)
	if len(input) == 0 {
		input = []codexInput{{
			Role:    "user",
			Content: "Continue.",
		}}
	}
	if strings.TrimSpace(instructions) == "" {
		instructions = defaultCodexInstructions
	}
	model, err := resolveCodexModel(request.Model)
	if err != nil {
		return nil, err
	}

	converted := codexRequest{
		Model:             model,
		Input:             input,
		Instructions:      instructions,
		Store:             false,
		Stream:            true,
		Temperature:       request.Temperature,
		TopP:              request.TopP,
		ToolChoice:        request.ToolChoice,
		ParallelToolCalls: request.ParallelTooCalls,
		Tools:             translateTools(request.Tools),
	}
	if request.MaxCompletionTokens != nil {
		converted.MaxOutputTokens = *request.MaxCompletionTokens
	} else if request.MaxTokens > 0 {
		converted.MaxOutputTokens = request.MaxTokens
	}
	if request.ReasoningEffort != nil && *request.ReasoningEffort != "" {
		converted.Reasoning = &reasoning{Effort: *request.ReasoningEffort}
	}
	return converted, nil
}

func (a *Adaptor) ConvertImageRequest(_ *relaymodel.ImageRequest) (any, error) {
	return nil, fmt.Errorf("OpenAI OAuth Codex channel does not support image generation")
}

func (a *Adaptor) DoRequest(c *gin.Context, meta *meta.Meta, requestBody io.Reader) (*http.Response, error) {
	return adaptor.DoRequestHelper(a, c, meta, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *meta.Meta) (*relaymodel.Usage, *relaymodel.ErrorWithStatusCode) {
	if meta.IsStream {
		return streamCodexResponse(c, resp, meta)
	}
	return nonStreamCodexResponse(c, resp, meta)
}

func (a *Adaptor) GetModelList() []string {
	models := make([]string, len(fallbackCodexModels))
	copy(models, fallbackCodexModels)
	return models
}

func (a *Adaptor) GetChannelName() string {
	return "OpenAI OAuth (Codex)"
}

func resolveCredential(meta *meta.Meta) (*openaioauth.Credential, error) {
	cred, err := openaioauth.ParseCredentialKey(meta.APIKey)
	if err != nil {
		return nil, err
	}
	if !cred.NeedsRefresh() || cred.RefreshToken == "" {
		return cred, nil
	}

	refreshed, err := openaioauth.RefreshAccessToken(cred, openaioauth.DefaultConfig())
	if err != nil {
		return nil, err
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = cred.AccountID
	}
	encoded, err := openaioauth.EncodeCredentialKey(refreshed)
	if err != nil {
		return nil, err
	}
	meta.APIKey = encoded
	if meta.ChannelId > 0 {
		if err := dbmodel.UpdateChannelKeyById(meta.ChannelId, encoded); err != nil {
			return nil, err
		}
	}
	return refreshed, nil
}

func translateMessages(messages []relaymodel.Message) ([]codexInput, string) {
	input := make([]codexInput, 0, len(messages))
	var instructions []string
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := message.StringContent()
		switch role {
		case "system", "developer":
			if strings.TrimSpace(content) != "" {
				instructions = append(instructions, content)
			}
		case "assistant":
			if strings.TrimSpace(content) != "" {
				input = append(input, codexInput{
					Role:    "assistant",
					Content: content,
				})
			}
			for _, toolCall := range message.ToolCalls {
				name, arguments, ok := resolveToolCall(toolCall)
				if !ok {
					continue
				}
				callID := toolCall.Id
				if callID == "" {
					callID = "call_" + uuid.NewString()
				}
				input = append(input, codexInput{
					Type:      "function_call",
					CallID:    callID,
					Name:      name,
					Arguments: arguments,
				})
			}
		case "tool":
			input = append(input, codexInput{
				Type:   "function_call_output",
				CallID: message.ToolCallId,
				Output: content,
			})
		default:
			input = append(input, codexInput{
				Role:    "user",
				Content: content,
			})
		}
	}
	return input, strings.Join(instructions, "\n\n")
}

func translateTools(tools []relaymodel.Tool) []codexTool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]codexTool, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		if tool.Function.Name == "" {
			continue
		}
		result = append(result, codexTool{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
			Strict:      false,
		})
	}
	return result
}

func resolveToolCall(toolCall relaymodel.Tool) (string, string, bool) {
	name := toolCall.Function.Name
	if name == "" {
		return "", "", false
	}
	switch args := toolCall.Function.Arguments.(type) {
	case string:
		if args == "" {
			return name, "{}", true
		}
		return name, args, true
	case nil:
		return name, "{}", true
	default:
		data, err := json.Marshal(args)
		if err != nil {
			return "", "", false
		}
		return name, string(data), true
	}
}

func resolveCodexModel(model string) (string, error) {
	m := strings.TrimSpace(model)
	if m == "" {
		return "", fmt.Errorf("model is required for OpenAI OAuth Codex channel")
	}
	lowerModel := strings.ToLower(m)
	if strings.HasPrefix(lowerModel, "openai/") {
		m = strings.TrimSpace(m[len("openai/"):])
		if m == "" {
			return "", fmt.Errorf("model is required for OpenAI OAuth Codex channel")
		}
		return m, nil
	}
	if strings.Contains(m, "/") {
		return "", fmt.Errorf("unsupported Codex model %q; use an OpenAI model id without provider prefix", m)
	}
	return m, nil
}

func streamCodexResponse(c *gin.Context, resp *http.Response, meta *meta.Meta) (*relaymodel.Usage, *relaymodel.ErrorWithStatusCode) {
	defer resp.Body.Close()
	common.SetEventStreamHeaders(c)

	created := time.Now().Unix()
	id := "chatcmpl-" + uuid.NewString()
	finishReason := "stop"
	var usage *relaymodel.Usage
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		payload := parseSSEData(scanner.Text())
		if payload == "" {
			continue
		}
		evt, err := parseCodexEvent(payload)
		if err != nil {
			continue
		}
		if evt.Response.ID != "" {
			id = evt.Response.ID
		}
		if evt.Response.Error != nil {
			return nil, codexErrorWrapper(evt.Response.Error, http.StatusBadGateway)
		}
		if evt.Response.Usage.TotalTokens > 0 {
			usage = usageFromCodex(evt.Response.Usage)
		}
		if reason := finishReasonFromStatus(evt.Response.Status); reason != "" {
			finishReason = reason
		}

		if delta := eventDelta(evt); delta != "" {
			if err := render.ObjectData(c, chatStreamResponse{
				ID:      id,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   meta.ActualModelName,
				Choices: []chatStreamChoice{{
					Index: 0,
					Delta: relaymodel.Message{Content: delta},
				}},
			}); err != nil {
				return nil, openai.ErrorWrapper(err, "stream_response_failed", http.StatusInternalServerError)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}

	if err := render.ObjectData(c, chatStreamResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   meta.ActualModelName,
		Choices: []chatStreamChoice{{
			Index:        0,
			Delta:        relaymodel.Message{},
			FinishReason: &finishReason,
		}},
		Usage: usage,
	}); err != nil {
		return nil, openai.ErrorWrapper(err, "stream_response_failed", http.StatusInternalServerError)
	}
	render.Done(c)
	if usage == nil {
		usage = &relaymodel.Usage{
			PromptTokens:     meta.PromptTokens,
			CompletionTokens: 0,
			TotalTokens:      meta.PromptTokens,
		}
	}
	return usage, nil
}

func nonStreamCodexResponse(c *gin.Context, resp *http.Response, meta *meta.Meta) (*relaymodel.Usage, *relaymodel.ErrorWithStatusCode) {
	defer resp.Body.Close()
	agg, err := aggregateCodexStream(resp.Body)
	if err != nil {
		return nil, openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	if agg.Err != nil {
		return nil, codexErrorWrapper(agg.Err, http.StatusBadGateway)
	}

	content := strings.TrimSpace(agg.Content.String())
	reasoningContent := strings.TrimSpace(agg.ReasoningContent.String())
	finishReason := agg.FinishReason
	if finishReason == "" {
		finishReason = "stop"
	}
	usage := agg.Usage
	if usage == nil {
		completionTokens := openai.CountTokenText(content, meta.ActualModelName)
		usage = &relaymodel.Usage{
			PromptTokens:     meta.PromptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      meta.PromptTokens + completionTokens,
		}
	}
	id := agg.ID
	if id == "" {
		id = "chatcmpl-" + uuid.NewString()
	}

	response := chatResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   meta.ActualModelName,
		Choices: []chatResponseChoice{{
			Index: 0,
			Message: relaymodel.Message{
				Role:             "assistant",
				Content:          content,
				ReasoningContent: reasoningContent,
				ToolCalls:        agg.ToolCalls,
			},
			FinishReason: finishReason,
		}},
		Usage: *usage,
	}
	c.JSON(http.StatusOK, response)
	return usage, nil
}

func aggregateCodexStream(body io.Reader) (*codexAggregate, error) {
	agg := &codexAggregate{}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		payload := parseSSEData(scanner.Text())
		if payload == "" {
			continue
		}
		evt, err := parseCodexEvent(payload)
		if err != nil {
			continue
		}
		accumulateEvent(agg, evt)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return agg, nil
}

func parseSSEData(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return ""
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload == "[DONE]" {
		return ""
	}
	return payload
}

func parseCodexEvent(payload string) (codexEvent, error) {
	var evt codexEvent
	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	decoder.UseNumber()
	err := decoder.Decode(&evt)
	return evt, err
}

func accumulateEvent(agg *codexAggregate, evt codexEvent) {
	if evt.Response.ID != "" {
		agg.ID = evt.Response.ID
	}
	if evt.Response.Error != nil {
		agg.Err = evt.Response.Error
	}
	if evt.Response.Usage.TotalTokens > 0 {
		agg.Usage = usageFromCodex(evt.Response.Usage)
	}
	if reason := finishReasonFromStatus(evt.Response.Status); reason != "" {
		agg.FinishReason = reason
	}
	if delta := eventDelta(evt); delta != "" {
		agg.Content.WriteString(delta)
	}
	if reasoning := eventReasoning(evt); reasoning != "" {
		agg.ReasoningContent.WriteString(reasoning)
	}
	if evt.Type == "response.completed" || evt.Type == "response.incomplete" || evt.Type == "response.failed" {
		accumulateResponseOutput(agg, evt.Response)
	}
	if evt.Type == "response.output_item.done" {
		accumulateItem(agg, evt.Item)
	}
}

func accumulateResponseOutput(agg *codexAggregate, response codexResponse) {
	for _, item := range response.Output {
		accumulateItem(agg, item)
	}
}

func accumulateItem(agg *codexAggregate, item codexItem) {
	switch item.Type {
	case "message":
		for _, content := range item.Content {
			switch content.Type {
			case "output_text":
				appendIfMissing(&agg.Content, content.Text)
			case "refusal":
				appendIfMissing(&agg.Content, content.Refusal)
			}
		}
	case "function_call":
		var args any = item.Arguments
		var parsedArgs any
		if err := json.Unmarshal([]byte(item.Arguments), &parsedArgs); err == nil {
			args = parsedArgs
		}
		agg.ToolCalls = append(agg.ToolCalls, relaymodel.Tool{
			Id:   item.CallID,
			Type: "function",
			Function: relaymodel.Function{
				Name:      item.Name,
				Arguments: args,
			},
		})
		agg.FinishReason = "tool_calls"
	}
}

func eventDelta(evt codexEvent) string {
	switch evt.Type {
	case "response.output_text.delta":
		return evt.Delta
	case "response.refusal.delta":
		return evt.Delta
	}
	return ""
}

func eventReasoning(evt codexEvent) string {
	switch evt.Type {
	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		return evt.Delta
	case "response.reasoning_text.done", "response.reasoning_summary_text.done":
		return evt.Text
	}
	return ""
}

func appendIfMissing(builder *strings.Builder, chunk string) {
	if chunk == "" {
		return
	}
	current := builder.String()
	if current == "" {
		builder.WriteString(chunk)
		return
	}
	if strings.Contains(chunk, current) {
		builder.Reset()
		builder.WriteString(chunk)
		return
	}
	if !strings.Contains(current, chunk) {
		builder.WriteString(chunk)
	}
}

func usageFromCodex(usage codexUsage) *relaymodel.Usage {
	return &relaymodel.Usage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
	}
}

func finishReasonFromStatus(status string) string {
	switch status {
	case "completed":
		return "stop"
	case "incomplete":
		return "length"
	case "failed":
		return "error"
	case "cancelled", "canceled":
		return "canceled"
	default:
		return ""
	}
}

func codexErrorWrapper(apiErr *codexAPIError, statusCode int) *relaymodel.ErrorWithStatusCode {
	message := "OpenAI Codex API error"
	if apiErr != nil && apiErr.Message != "" {
		message = apiErr.Message
	}
	errType := "codex_api_error"
	code := "codex_api_error"
	if apiErr != nil {
		if apiErr.Type != "" {
			errType = apiErr.Type
		}
		if apiErr.Code != "" {
			code = apiErr.Code
		}
	}
	return &relaymodel.ErrorWithStatusCode{
		Error: relaymodel.Error{
			Message: message,
			Type:    errType,
			Code:    code,
		},
		StatusCode: statusCode,
	}
}

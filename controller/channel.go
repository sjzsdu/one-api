package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/client"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/openaioauth"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/apitype"
	"github.com/songquanpeng/one-api/relay/channeltype"
)

type refreshChannelModelsRequest struct {
	Id      int    `json:"id"`
	Type    int    `json:"type"`
	Key     string `json:"key"`
	BaseURL string `json:"base_url"`
	Other   string `json:"other"`
	Config  string `json:"config"`
}

type upstreamModelsResponse struct {
	Data   []upstreamModel `json:"data"`
	Models []upstreamModel `json:"models"`
	Error  any             `json:"error"`
}

type upstreamModel struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model"`
	Slug  string `json:"slug"`
}

const codexModelListUnavailableMessage = "无法自动获取 Codex OAuth 模型列表：上游未提供可枚举的模型目录。请在模型框中直接输入模型名并回车添加。"

func normalizeModelNames(models []string) []string {
	seen := make(map[string]bool)
	normalizedModels := make([]string, 0, len(models))
	for _, modelName := range models {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" || seen[modelName] {
			continue
		}
		seen[modelName] = true
		normalizedModels = append(normalizedModels, modelName)
	}
	return normalizedModels
}

func resolveChannelBaseURL(channelType int, baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL != "" {
		return baseURL
	}
	if channelType > 0 && channelType < len(channeltype.ChannelBaseURLs) {
		return strings.TrimRight(channeltype.ChannelBaseURLs[channelType], "/")
	}
	return ""
}

func uniqueURLs(urls ...string) []string {
	seen := make(map[string]bool)
	unique := make([]string, 0, len(urls))
	for _, url := range urls {
		url = strings.TrimSpace(url)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		unique = append(unique, url)
	}
	return unique
}

func buildOpenAICompatibleModelURLs(channelType int, baseURL string) []string {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return nil
	}

	modelsURL := baseURL + "/models"
	v1ModelsURL := baseURL + "/v1/models"
	if channelType == channeltype.OpenAICompatible ||
		strings.HasSuffix(baseURL, "/v1") ||
		strings.HasSuffix(baseURL, "/v1beta/openai") ||
		strings.HasSuffix(baseURL, "/v3/openai") {
		return uniqueURLs(modelsURL, v1ModelsURL)
	}
	return uniqueURLs(v1ModelsURL, modelsURL)
}

func parseUpstreamModels(resp *http.Response) ([]string, string, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, "", err
	}

	var upstreamResponse upstreamModelsResponse
	trimmedBody := strings.TrimSpace(string(body))
	if trimmedBody == "" {
		errorMessage := "上游模型列表接口返回空响应"
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			errorMessage = fmt.Sprintf("上游返回状态码 %d，%s", resp.StatusCode, errorMessage)
		}
		return nil, errorMessage, fmt.Errorf("%s", errorMessage)
	}
	if !strings.HasPrefix(trimmedBody, "{") && !strings.HasPrefix(trimmedBody, "[") {
		errorMessage := "上游模型列表接口返回的不是 JSON"
		if strings.HasPrefix(trimmedBody, "<") {
			errorMessage = "上游模型列表接口返回了 HTML 页面，不是 JSON"
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			errorMessage = fmt.Sprintf("上游返回状态码 %d，%s", resp.StatusCode, errorMessage)
		}
		return nil, errorMessage, fmt.Errorf("%s", errorMessage)
	}
	if strings.HasPrefix(trimmedBody, "[") {
		err = json.Unmarshal(body, &upstreamResponse.Data)
	} else {
		err = json.Unmarshal(body, &upstreamResponse)
	}
	if err != nil {
		errorMessage := fmt.Sprintf("解析上游模型列表 JSON 失败：%s", err.Error())
		return nil, errorMessage, fmt.Errorf("%s", errorMessage)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		errorMessage := fmt.Sprintf("上游返回状态码 %d", resp.StatusCode)
		if upstreamResponse.Error != nil {
			if errorBytes, err := json.Marshal(upstreamResponse.Error); err == nil {
				errorMessage = fmt.Sprintf("%s：%s", errorMessage, string(errorBytes))
			}
		}
		return nil, errorMessage, fmt.Errorf("%s", errorMessage)
	}

	upstreamModels := upstreamResponse.Data
	if len(upstreamModels) == 0 {
		upstreamModels = upstreamResponse.Models
	}
	modelNames := make([]string, 0, len(upstreamModels))
	for _, upstreamModel := range upstreamModels {
		switch {
		case upstreamModel.Id != "":
			modelNames = append(modelNames, upstreamModel.Id)
		case upstreamModel.Name != "":
			modelNames = append(modelNames, upstreamModel.Name)
		case upstreamModel.Model != "":
			modelNames = append(modelNames, upstreamModel.Model)
		case upstreamModel.Slug != "":
			modelNames = append(modelNames, upstreamModel.Slug)
		}
	}
	return normalizeModelNames(modelNames), "", nil
}

func isModelListFormatError(message string) bool {
	return strings.Contains(message, "不是 JSON") ||
		strings.Contains(message, "空响应") ||
		strings.Contains(message, "解析上游模型列表 JSON 失败")
}

func requestUpstreamModels(url string, key string, channelType int) ([]string, string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	if channelType == channeltype.Azure {
		req.Header.Set("api-key", key)
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.ImpatientHTTPClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	return parseUpstreamModels(resp)
}

func refreshOpenAICompatibleModels(channelType int, baseURL string, key string) ([]string, string, error) {
	var lastMessage string
	var lastErr error
	for _, url := range buildOpenAICompatibleModelURLs(channelType, baseURL) {
		models, message, err := requestUpstreamModels(url, key, channelType)
		if err == nil && len(models) > 0 {
			return models, url, nil
		}
		if message != "" {
			lastMessage = message
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastMessage != "" {
		return nil, "", fmt.Errorf("%s", lastMessage)
	}
	if lastErr != nil {
		return nil, "", lastErr
	}
	return nil, "", fmt.Errorf("上游未返回模型列表")
}

func refreshAzureModels(baseURL string, key string, apiVersion string) ([]string, string, error) {
	if apiVersion == "" {
		apiVersion = "2024-03-01-preview"
	}
	url := fmt.Sprintf("%s/openai/deployments?api-version=%s", baseURL, apiVersion)
	models, _, err := requestUpstreamModels(url, key, channeltype.Azure)
	return models, url, err
}

func refreshOpenAICodexModels(channelId int, baseURL string, key string) ([]string, string, error) {
	cred, err := openaioauth.ParseCredentialKey(key)
	if err != nil {
		return nil, "", err
	}
	if cred.NeedsRefresh() && cred.RefreshToken != "" {
		refreshed, err := openaioauth.RefreshAccessToken(cred, openaioauth.DefaultConfig())
		if err != nil {
			return nil, "", err
		}
		if refreshed.AccountID == "" {
			refreshed.AccountID = cred.AccountID
		}
		encoded, err := openaioauth.EncodeCredentialKey(refreshed)
		if err != nil {
			return nil, "", err
		}
		key = encoded
		cred = refreshed
		if channelId > 0 {
			_ = model.UpdateChannelKeyById(channelId, encoded)
		}
	}

	var lastErr error
	modelListUnavailable := false
	for _, url := range uniqueURLs(
		strings.TrimRight(baseURL, "/")+"/models",
		strings.TrimRight(baseURL, "/")+"/v1/models",
		strings.TrimRight(baseURL, "/")+"/responses/models",
	) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
		req.Header.Set("originator", "codex_cli_rs")
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		if cred.AccountID != "" {
			req.Header.Set("Chatgpt-Account-Id", cred.AccountID)
		}
		resp, err := client.ImpatientHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		statusCode := resp.StatusCode
		models, message, err := parseUpstreamModels(resp)
		if err == nil && len(models) > 0 {
			return models, url, nil
		}
		if err != nil {
			if isModelListFormatError(message) || statusCode == http.StatusNotFound {
				modelListUnavailable = true
				continue
			}
			lastErr = err
		}
	}
	if modelListUnavailable {
		return nil, "", fmt.Errorf("%s", codexModelListUnavailableMessage)
	}
	if lastErr != nil {
		return nil, "", lastErr
	}
	return nil, "", fmt.Errorf("%s", codexModelListUnavailableMessage)
}

func GetAllChannels(c *gin.Context) {
	p, _ := strconv.Atoi(c.Query("p"))
	if p < 0 {
		p = 0
	}
	channels, err := model.GetAllChannels(p*config.ItemsPerPage, config.ItemsPerPage, "limited")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channels,
	})
	return
}

func SearchChannels(c *gin.Context) {
	keyword := c.Query("keyword")
	channels, err := model.SearchChannels(keyword)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channels,
	})
	return
}

func RefreshChannelModels(c *gin.Context) {
	req := refreshChannelModelsRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if req.Id != 0 {
		channel, err := model.GetChannelById(req.Id, true)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		if req.Type == 0 {
			req.Type = channel.Type
		}
		if req.Key == "" {
			req.Key = channel.Key
		}
		if req.BaseURL == "" && channel.BaseURL != nil {
			req.BaseURL = *channel.BaseURL
		}
		if req.Other == "" && channel.Other != nil {
			req.Other = *channel.Other
		}
		if req.Config == "" {
			req.Config = channel.Config
		}
	}

	req.Key = strings.TrimSpace(req.Key)
	if req.Type == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "请先选择渠道类型",
		})
		return
	}
	if req.Key == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "请先填写渠道密钥",
		})
		return
	}

	baseURL := resolveChannelBaseURL(req.Type, req.BaseURL)
	if baseURL == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "当前渠道未配置 Base URL，无法从上游刷新模型",
		})
		return
	}

	var models []string
	var sourceURL string
	var err error
	if req.Type == channeltype.Azure {
		models, sourceURL, err = refreshAzureModels(baseURL, req.Key, req.Other)
	} else if req.Type == channeltype.OpenAICodexOAuth {
		models, sourceURL, err = refreshOpenAICodexModels(req.Id, baseURL, req.Key)
	} else if channeltype.ToAPIType(req.Type) == apitype.OpenAI {
		models, sourceURL, err = refreshOpenAICompatibleModels(req.Type, baseURL, req.Key)
	} else {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "当前渠道类型没有通用模型列表接口，请继续使用内置列表或手动添加模型",
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if len(models) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "上游未返回模型列表",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"models":     models,
			"source_url": sourceURL,
		},
	})
}

func GetChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel, err := model.GetChannelById(id, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

func AddChannel(c *gin.Context) {
	channel := model.Channel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel.CreatedTime = helper.GetTimestamp()
	keys := strings.Split(channel.Key, "\n")
	channels := make([]model.Channel, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		localChannel := channel
		localChannel.Key = key
		channels = append(channels, localChannel)
	}
	err = model.BatchInsertChannels(channels)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	channel := model.Channel{Id: id}
	err := channel.Delete()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteDisabledChannel(c *gin.Context) {
	rows, err := model.DeleteDisabledChannel()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func UpdateChannel(c *gin.Context) {
	channel := model.Channel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	err = channel.Update()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

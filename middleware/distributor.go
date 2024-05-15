package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channeltype"
)

type ModelRequest struct {
	Model string `json:"model"`
}

func Distribute() func(c *gin.Context) {
	return func(c *gin.Context) {
		userId := c.GetInt(ctxkey.Id)
		userGroup, _ := model.CacheGetUserGroup(userId)
		c.Set(ctxkey.Group, userGroup)
		// 在不影响后续通过c.Request.Body 获取 body 的情况下，将 body 读取出来
		// 读取 body
		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(c.Request.Body)
		}

		// 将 body 内容重新设置回 c.Request.Body
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// 解析 body 到请求的结构体

		// 解析 JSON 请求数据
		// 检查是否有 messages 字段并解析
		isImage := false
		var requestData map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &requestData); err != nil {
			fmt.Println("解析 JSON 请求数据失败")
		} else {
			if messages, ok := requestData["messages"].([]interface{}); ok {
				for _, message := range messages {
					if msgMap, ok := message.(map[string]interface{}); ok {
						if content, exists := msgMap["content"]; exists {
							switch content.(type) {
							case []interface{}:
								for _, item := range content.([]interface{}) {
									if itemMap, ok := item.(map[string]interface{}); ok {
										if t, exists := itemMap["type"]; exists && t == "image_url" {
											isImage = true
											break
										}
									}
								}
							}
							if isImage {
								break
							}
						}
					}
				}
			}
		}

		var requestModel string
		var channel *model.Channel
		channelId, ok := c.Get(ctxkey.SpecificChannelId)
		if ok {
			id, err := strconv.Atoi(channelId.(string))
			if err != nil {
				abortWithMessage(c, http.StatusBadRequest, "无效的渠道 Id")
				return
			}
			channel, err = model.GetChannelById(id, true)
			if err != nil {
				abortWithMessage(c, http.StatusBadRequest, "无效的渠道 Id")
				return
			}
			if channel.Status != model.ChannelStatusEnabled {
				abortWithMessage(c, http.StatusForbidden, "该渠道已被禁用")
				return
			}
		} else {
			requestModel = c.GetString(ctxkey.RequestModel)
			var err error
			channel, err = model.CacheGetRandomSatisfiedChannel(userGroup, requestModel, false, isImage)
			if err != nil {
				message := fmt.Sprintf("当前分组 %s 下对于模型 %s 无可用渠道", userGroup, requestModel)
				if channel != nil {
					logger.SysError(fmt.Sprintf("渠道不存在：%d", channel.Id))
					message = "数据库一致性已被破坏，请联系管理员"
				}
				abortWithMessage(c, http.StatusServiceUnavailable, message)
				return
			}
		}
		SetupContextForSelectedChannel(c, channel, requestModel)
		c.Next()
	}
}

func SetupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string) {
	c.Set(ctxkey.Channel, channel.Type)
	c.Set(ctxkey.ChannelId, channel.Id)
	c.Set(ctxkey.ChannelName, channel.Name)
	c.Set(ctxkey.ModelMapping, channel.GetModelMapping())
	c.Set(ctxkey.OriginalModel, modelName) // for retry
	c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", channel.Key))
	c.Set(ctxkey.BaseURL, channel.GetBaseURL())
	cfg, _ := channel.LoadConfig()
	// this is for backward compatibility
	switch channel.Type {
	case channeltype.Azure:
		if cfg.APIVersion == "" {
			cfg.APIVersion = channel.Other
		}
	case channeltype.Xunfei:
		if cfg.APIVersion == "" {
			cfg.APIVersion = channel.Other
		}
	case channeltype.Gemini:
		if cfg.APIVersion == "" {
			cfg.APIVersion = channel.Other
		}
	case channeltype.AIProxyLibrary:
		if cfg.LibraryID == "" {
			cfg.LibraryID = channel.Other
		}
	case channeltype.Ali:
		if cfg.Plugin == "" {
			cfg.Plugin = channel.Other
		}
	}
	c.Set(ctxkey.Config, cfg)
}

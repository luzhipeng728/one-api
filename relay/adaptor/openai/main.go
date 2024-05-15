package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/conv"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

const (
	dataPrefix       = "data: "
	done             = "[DONE]"
	dataPrefixLength = len(dataPrefix)
)

var modelmapper = map[string]string{
	"gpt-4-turbo":         "gpt-4-turbo-2024-04-09",
	"gpt-4":               "gpt-4-0613",
	"gpt-3.5-turbo":       "gpt-3.5-turbo-0613",
	"gpt-3.5-turbo-16k":   "gpt-3.5-turbo-16k-0613",
	"gpt-4-32k":           "gpt-4-32k-0613",
	"gpt-4-turbo-preview": "gpt-4-0125-preview",
}

func StreamHandler(c *gin.Context, resp *http.Response, relayMode int, originModelNmae string) (*model.ErrorWithStatusCode, string, *model.Usage) {
	// 在modelmapper中查找对应的模型名称,如果不存在，就用originModelNmae，否则使用modelmapper中的模型名称
	modelName := originModelNmae
	if v, ok := modelmapper[originModelNmae]; ok {
		fmt.Println("modelName is in modelmapper change to ", v)
		modelName = v
	}
	responseText := ""
	scanner := bufio.NewScanner(resp.Body)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := strings.Index(string(data), "\n"); i >= 0 {
			return i + 1, data[0:i], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	dataChan := make(chan string)
	stopChan := make(chan bool)
	var usage *model.Usage
	go func() {
		for scanner.Scan() {
			data := scanner.Text()
			if len(data) < dataPrefixLength { // ignore blank line or wrong format
				continue
			}
			if data[:dataPrefixLength] != dataPrefix && data[:dataPrefixLength] != done {
				continue
			}
			if strings.HasPrefix(data[dataPrefixLength:], done) {
				dataChan <- data
				continue
			}
			// 去掉前面的 "data: "
			jsonData := data[dataPrefixLength:]

			// 将数据转换为 JSON 对象
			var jsonObj map[string]interface{}
			err := json.Unmarshal([]byte(jsonData), &jsonObj)
			if err != nil {
				logger.SysError("error unmarshalling stream response: " + err.Error())
				dataChan <- data // if error happened, pass the data to client
				continue         // just ignore the error
			}

			// 删除每个 choice 中的 content_filter_results 字段
			if choices, ok := jsonObj["choices"].([]interface{}); ok {
				for _, choice := range choices {
					if choiceMap, ok := choice.(map[string]interface{}); ok {
						delete(choiceMap, "content_filter_results")
					}
				}
			}
			// 修改下model名称
			jsonObj["model"] = modelName
			// 将修改后的 JSON 对象重新转换为字符串
			modifiedData, err := json.Marshal(jsonObj)
			if err != nil {
				logger.SysError("error marshalling modified stream response: " + err.Error())
				dataChan <- data // if error happened, pass the data to client
				continue         // just ignore the error
			}

			modifiedDataStr := dataPrefix + string(modifiedData)

			switch relayMode {
			case relaymode.ChatCompletions:
				var streamResponse ChatCompletionsStreamResponse
				err := json.Unmarshal([]byte(jsonData), &streamResponse)
				if err != nil {
					logger.SysError("error unmarshalling stream response: " + err.Error())
					dataChan <- modifiedDataStr // if error happened, pass the data to client
					continue                    // just ignore the error
				}
				if len(streamResponse.Choices) == 0 {
					// but for empty choice, we should not pass it to client, this is for azure
					continue // just ignore empty choice
				}
				dataChan <- modifiedDataStr
				for _, choice := range streamResponse.Choices {
					responseText += conv.AsString(choice.Delta.Content)
				}
				if streamResponse.Usage != nil {
					usage = streamResponse.Usage
				}
			case relaymode.Completions:
				dataChan <- modifiedDataStr
				var streamResponse CompletionsStreamResponse
				err := json.Unmarshal([]byte(jsonData), &streamResponse)
				if err != nil {
					logger.SysError("error unmarshalling stream response: " + err.Error())
					continue
				}
				for _, choice := range streamResponse.Choices {
					responseText += choice.Text
				}
			}
		}
		stopChan <- true
	}()
	common.SetEventStreamHeaders(c)
	c.Stream(func(w io.Writer) bool {
		select {
		case data := <-dataChan:
			if strings.HasPrefix(data, "data: [DONE]") {
				data = data[:12]
			}
			// some implementations may add \r at the end of data
			data = strings.TrimSuffix(data, "\r")
			c.Render(-1, common.CustomEvent{Data: data})
			return true
		case <-stopChan:
			return false
		}
	})
	err := resp.Body.Close()
	if err != nil {
		return ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), "", nil
	}
	return nil, responseText, usage
}

// func StreamHandler(c *gin.Context, resp *http.Response, relayMode int) (*model.ErrorWithStatusCode, string, *model.Usage) {
// 	responseText := ""
// 	scanner := bufio.NewScanner(resp.Body)
// 	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
// 		if atEOF && len(data) == 0 {
// 			return 0, nil, nil
// 		}
// 		if i := strings.Index(string(data), "\n"); i >= 0 {
// 			return i + 1, data[0:i], nil
// 		}
// 		if atEOF {
// 			return len(data), data, nil
// 		}
// 		return 0, nil, nil
// 	})
// 	dataChan := make(chan string)
// 	stopChan := make(chan bool)
// 	var usage *model.Usage
// 	go func() {
// 		for scanner.Scan() {
// 			data := scanner.Text()
// 			if len(data) < dataPrefixLength { // ignore blank line or wrong format
// 				continue
// 			}
// 			if data[:dataPrefixLength] != dataPrefix && data[:dataPrefixLength] != done {
// 				continue
// 			}
// 			if strings.HasPrefix(data[dataPrefixLength:], done) {
// 				dataChan <- data
// 				continue
// 			}
// 			switch relayMode {
// 			case relaymode.ChatCompletions:
// 				var streamResponse ChatCompletionsStreamResponse
// 				err := json.Unmarshal([]byte(data[dataPrefixLength:]), &streamResponse)
// 				if err != nil {
// 					logger.SysError("error unmarshalling stream response: " + err.Error())
// 					dataChan <- data // if error happened, pass the data to client
// 					continue         // just ignore the error
// 				}
// 				if len(streamResponse.Choices) == 0 {
// 					// but for empty choice, we should not pass it to client, this is for azure
// 					continue // just ignore empty choice
// 				}
// 				dataChan <- data
// 				for _, choice := range streamResponse.Choices {
// 					responseText += conv.AsString(choice.Delta.Content)
// 				}
// 				if streamResponse.Usage != nil {
// 					usage = streamResponse.Usage
// 				}
// 			case relaymode.Completions:
// 				dataChan <- data
// 				var streamResponse CompletionsStreamResponse
// 				err := json.Unmarshal([]byte(data[dataPrefixLength:]), &streamResponse)
// 				if err != nil {
// 					logger.SysError("error unmarshalling stream response: " + err.Error())
// 					continue
// 				}
// 				for _, choice := range streamResponse.Choices {
// 					responseText += choice.Text
// 				}
// 			}
// 		}
// 		stopChan <- true
// 	}()
// 	common.SetEventStreamHeaders(c)
// 	c.Stream(func(w io.Writer) bool {
// 		select {
// 		case data := <-dataChan:
// 			if strings.HasPrefix(data, "data: [DONE]") {
// 				data = data[:12]
// 			}
// 			// some implementations may add \r at the end of data
// 			data = strings.TrimSuffix(data, "\r")
// 			c.Render(-1, common.CustomEvent{Data: data})
// 			return true
// 		case <-stopChan:
// 			return false
// 		}
// 	})
// 	err := resp.Body.Close()
// 	if err != nil {
// 		return ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), "", nil
// 	}
// 	return nil, responseText, usage
// }

func Handler(c *gin.Context, resp *http.Response, promptTokens int, modelName string) (*model.ErrorWithStatusCode, *model.Usage) {
	// 在modelmapper中查找对应的模型名称,如果不存在，就用originModelNmae，否则使用modelmapper中的模型名称
	if v, ok := modelmapper[modelName]; ok {
		fmt.Println("modelName is in modelmapper change to ", v)
		modelName = v
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
	}
	err = resp.Body.Close()
	if err != nil {
		return ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}

	var jsonResponse map[string]interface{}
	err = json.Unmarshal(responseBody, &jsonResponse)
	if err != nil {
		return ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError), nil
	}

	if errInfo, ok := jsonResponse["error"]; ok {
		if errType, exists := errInfo.(map[string]interface{})["type"]; exists && errType != "" {
			return &model.ErrorWithStatusCode{
				Error:      model.Error{Type: errType.(string)},
				StatusCode: resp.StatusCode,
			}, nil
		}
	}

	// 删除 choices 中的 content_filter_results 字段
	if choices, ok := jsonResponse["choices"].([]interface{}); ok {
		for _, choice := range choices {
			if choiceMap, ok := choice.(map[string]interface{}); ok {
				delete(choiceMap, "content_filter_results")
			}
		}
	}

	// 删除外层的 prompt_filter_results 字段
	delete(jsonResponse, "prompt_filter_results")
	// 修改下model名称
	jsonResponse["model"] = modelName

	// 将修改后的 JSON 对象重新转换为字符串
	modifiedResponseBody, err := json.Marshal(jsonResponse)
	if err != nil {
		return ErrorWrapper(err, "marshal_modified_response_body_failed", http.StatusInternalServerError), nil
	}

	// 重置响应体
	resp.Body = io.NopCloser(bytes.NewBuffer(modifiedResponseBody))

	for k, v := range resp.Header {
		c.Writer.Header().Set(k, v[0])
	}
	// 重新计算 Content-Length 并设置响应头
	c.Writer.Header().Set("Content-Length", fmt.Sprint(len(modifiedResponseBody)))
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = io.Copy(c.Writer, resp.Body)
	if err != nil {
		return ErrorWrapper(err, "copy_response_body_failed", http.StatusInternalServerError), nil
	}

	err = resp.Body.Close()
	if err != nil {
		return ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}

	if usage, ok := jsonResponse["usage"].(map[string]interface{}); ok {
		if totalTokens, exists := usage["total_tokens"].(float64); exists && totalTokens == 0 {
			completionTokens := 0
			if choices, ok := jsonResponse["choices"].([]interface{}); ok {
				for _, choice := range choices {
					if choiceMap, ok := choice.(map[string]interface{}); ok {
						if message, exists := choiceMap["message"]; exists {
							if messageMap, ok := message.(map[string]interface{}); ok {
								if content, exists := messageMap["content"]; exists {
									completionTokens += CountTokenText(content.(string), modelName)
								}
							}
						}
					}
				}
			}
			jsonResponse["usage"] = map[string]interface{}{
				"prompt_tokens":     promptTokens,
				"completion_tokens": completionTokens,
				"total_tokens":      promptTokens + completionTokens,
			}
		}
	}
	return nil, &model.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: int(jsonResponse["usage"].(map[string]interface{})["completion_tokens"].(float64)),
		TotalTokens:      int(jsonResponse["usage"].(map[string]interface{})["total_tokens"].(float64)),
	}
}

// func Handler(c *gin.Context, resp *http.Response, promptTokens int, modelName string) (*model.ErrorWithStatusCode, *model.Usage) {
// 	var textResponse SlimTextResponse
// 	responseBody, err := io.ReadAll(resp.Body)
// 	if err != nil {
// 		return ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
// 	}
// 	err = resp.Body.Close()
// 	if err != nil {
// 		return ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
// 	}
// 	err = json.Unmarshal(responseBody, &textResponse)
// 	if err != nil {
// 		return ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError), nil
// 	}
// 	if textResponse.Error.Type != "" {
// 		return &model.ErrorWithStatusCode{
// 			Error:      textResponse.Error,
// 			StatusCode: resp.StatusCode,
// 		}, nil
// 	}
// 	// Reset response body
// 	resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))

// 	// We shouldn't set the header before we parse the response body, because the parse part may fail.
// 	// And then we will have to send an error response, but in this case, the header has already been set.
// 	// So the HTTPClient will be confused by the response.
// 	// For example, Postman will report error, and we cannot check the response at all.
// 	for k, v := range resp.Header {
// 		c.Writer.Header().Set(k, v[0])
// 	}
// 	c.Writer.WriteHeader(resp.StatusCode)
// 	_, err = io.Copy(c.Writer, resp.Body)
// 	if err != nil {
// 		return ErrorWrapper(err, "copy_response_body_failed", http.StatusInternalServerError), nil
// 	}
// 	err = resp.Body.Close()
// 	if err != nil {
// 		return ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
// 	}

// 	if textResponse.Usage.TotalTokens == 0 {
// 		completionTokens := 0
// 		for _, choice := range textResponse.Choices {
// 			completionTokens += CountTokenText(choice.Message.StringContent(), modelName)
// 		}
// 		textResponse.Usage = model.Usage{
// 			PromptTokens:     promptTokens,
// 			CompletionTokens: completionTokens,
// 			TotalTokens:      promptTokens + completionTokens,
// 		}
// 	}
// 	return nil, &textResponse.Usage
// }

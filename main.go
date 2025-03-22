package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// API配置
const (
	OpenRouterAPIURL = "https://openrouter.ai/api/v1/chat/completions"
)

// Message 表示对话中的一条消息
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenRouterRequest 表示发送到OpenRouter的请求结构
type OpenRouterRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// StreamChunk 表示流式响应中的一个数据块
type StreamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int    `json:"index"`
		Delta        Delta  `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// Delta 表示流式响应中的内容差异
type Delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// ChatRequest 是API的请求结构
type ChatRequest struct {
	Messages []Message `json:"messages" binding:"required"`
	Model    string    `json:"model" binding:"required"`
}

// OpenRouterClient 是OpenRouter API的客户端封装
type OpenRouterClient struct {
	APIURL  string
	APIKey  string
	Client  *http.Client
	Headers map[string]string
}

// NewOpenRouterClient 创建一个新的OpenRouter客户端
func NewOpenRouterClient(apiKey string) *OpenRouterClient {
	return &OpenRouterClient{
		APIURL: OpenRouterAPIURL,
		APIKey: apiKey,
		Client: &http.Client{},
		Headers: map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + apiKey,
			"HTTP-Referer":  "https://data-planet.example.com", // 替换为您的应用域名
			"X-Title":       "Data Planet",                     // 替换为您的应用名称
		},
	}
}

// StreamCompletion 以流式方式调用OpenRouter API并处理响应
func (c *OpenRouterClient) StreamCompletion(
	model string,
	messages []Message,
	writer io.Writer,
) error {
	// 构建请求体
	req := OpenRouterRequest{
		Model:       model,
		Messages:    messages,
		Stream:      true,
		Temperature: 0.7,  // 使用默认温度
		MaxTokens:   2000, // 使用默认最大令牌数
	}

	// 序列化请求体
	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	// 创建HTTP请求
	httpReq, err := http.NewRequest("POST", c.APIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	for key, value := range c.Headers {
		httpReq.Header.Set(key, value)
	}

	// 发送请求
	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API错误: %s, 状态码: %d", string(body), resp.StatusCode)
	}

	// 解析流式响应
	reader := bufio.NewReader(resp.Body)
	for {
		// 读取一行数据
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("读取响应失败: %w", err)
		}

		// 跳过空行
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// 处理SSE前缀
		const ssePrefix = "data: "
		if !bytes.HasPrefix(line, []byte(ssePrefix)) {
			continue
		}

		// 将完整的SSE行写入输出
		if _, err := fmt.Fprintf(writer, "data: %s\n\n", bytes.TrimPrefix(line, []byte(ssePrefix))); err != nil {
			return fmt.Errorf("写入响应失败: %w", err)
		}

		// 如果writer支持刷新，则刷新缓冲区
		if f, ok := writer.(http.Flusher); ok {
			f.Flush()
		}

		// 检查流是否结束
		if string(bytes.TrimPrefix(line, []byte(ssePrefix))) == "[DONE]" {
			break
		}
	}

	return nil
}

func main() {
	// 从环境变量获取API密钥
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		log.Fatal("环境变量 OPENROUTER_API_KEY 未设置")
	}

	// 创建OpenRouter客户端
	client := NewOpenRouterClient(apiKey)

	// 创建Gin服务器
	r := gin.Default()

	// CORS中间件
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	// 健康检查API
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	// 实现聊天API
	r.POST("/chat/v1", func(c *gin.Context) {
		var req ChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
			return
		}

		// 验证请求参数
		if len(req.Messages) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "消息不能为空"})
			return
		}

		if req.Model == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "模型不能为空"})
			return
		}

		// 设置SSE响应头
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")

		// 调用OpenRouter API并返回流式响应
		if err := client.StreamCompletion(req.Model, req.Messages, c.Writer); err != nil {
			// 注意：在开始发送SSE响应后，无法再发送正常的HTTP错误响应
			// 所以我们发送一个特殊的SSE消息表示错误
			log.Printf("流式响应错误: %v", err)
			c.Writer.Write([]byte(fmt.Sprintf("data: {\"error\": \"%v\"}\n\n", err)))
		}
	})

	// 启动服务器
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("服务器启动在 http://localhost:%s", port)
	log.Printf("访问 /chat/v1 接口进行聊天")

	if err := r.Run(":" + port); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}

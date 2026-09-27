package modes

import (
	"ai-router-gateway/internal/models"
)

// HeadroomPrompt returns a system prompt that instructs the model to be mindful of context length.
func HeadroomPrompt() string {
	return "You are operating in headroom mode. Keep responses concise and focused. Avoid unnecessary elaboration. Prioritize the most relevant information."
}

// ApplyHeadroom trims conversation history to stay within a token budget.
// It preserves the system prompt and the most recent messages, removing older ones first.
// If tool_result messages are present, it compresses them before trimming.
func ApplyHeadroom(messages []models.Message, maxTokens int) []models.Message {
	if maxTokens <= 0 || len(messages) <= 3 {
		return messages
	}

	// Estimate current token count (rough: ~4 chars per token)
	currentTokens := 0
	for _, msg := range messages {
		currentTokens += len(msg.Content.String()) / 4
	}

	// If we're under budget, no trimming needed
	if currentTokens <= maxTokens {
		return messages
	}

	// Build result preserving system + most recent messages
	var result []models.Message
	var systemMsg models.Message
	var recentMsgs []models.Message

	// Separate system message and recent messages
	for i, msg := range messages {
		if msg.Role == "system" && i == 0 {
			systemMsg = msg
		} else {
			recentMsgs = append(recentMsgs, msg)
		}
	}

	// Start from the end and keep adding until we hit the budget
	tokensUsed := 0
	if systemMsg.Content.String() != "" {
		tokensUsed += len(systemMsg.Content.String()) / 4
	}

	trimmed := make([]models.Message, 0)
	for i := len(recentMsgs) - 1; i >= 0; i-- {
		msgTokens := len(recentMsgs[i].Content.String()) / 4
		if tokensUsed+msgTokens > maxTokens && len(trimmed) > 0 {
			break
		}
		trimmed = append([]models.Message{recentMsgs[i]}, trimmed...)
		tokensUsed += msgTokens
	}

	result = append(result, systemMsg)
	result = append(result, trimmed...)
	return result
}

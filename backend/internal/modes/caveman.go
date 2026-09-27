package modes

import "ai-router-gateway/internal/models"

func CavemanPrompt(level int) string {
	switch level {
	case 1:
		return "Be concise. Use short sentences. Skip pleasantries."
	case 2:
		return "Be extremely brief. Give only the answer. No explanations unless asked."
	case 3:
		return "Reply with code only. Skip all text. No markdown blocks unless needed."
	case 4:
		return "You are a caveman. Answer in 1-3 words. Only code, no talk."
	case 5:
		return "SILENCE. Output is forbidden unless essential. Respond with absolute minimum."
	default:
		return "Be concise. Use short sentences. Skip pleasantries."
	}
}

func ApplyCaveman(messages []models.Message, level int) []models.Message {
	cavemanPrompt := CavemanPrompt(level)
	var result []models.Message

	foundSystem := false
	for _, msg := range messages {
		if msg.Role == "system" && !foundSystem {
			msg.Content = models.MessageContent(cavemanPrompt + "\n\n" + msg.Content.String())
			foundSystem = true
		}
		if msg.Role == "user" && !foundSystem {
			result = append(result, models.Message{Role: "system", Content: models.MessageContent(cavemanPrompt)})
			foundSystem = true
		}
		result = append(result, msg)
	}

	if !foundSystem {
		result = append([]models.Message{{Role: "system", Content: models.MessageContent(cavemanPrompt)}}, result...)
	}

	return result
}

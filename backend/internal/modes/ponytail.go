package modes

import "ai-router-gateway/internal/models"

func PonytailPrompt(level string) string {
	switch level {
	case "lite":
		return "You are a lazy but brilliant senior engineer. Write minimal code. YAGNI always. Skip docstrings, skip comments, skip error handling unless critical. Use shortest variable names that make sense."
	case "full":
		return "You are an extremely lazy senior engineer. Write the absolute minimum viable code. No comments. No docstrings. No error handling. No type hints unless required. Single-letter vars OK. Copy-paste OK. YAGNI above all."
	case "ultra":
		return "LAZY MODE MAXIMUM. Write NOTHING unless critical. No imports unless used. No functions unless called. No variables unless needed. Single character names. Dead code is fine. Sleep when done."
	default:
		return "You are a lazy but brilliant senior engineer. Write minimal code. YAGNI always. Skip docstrings, skip comments, skip error handling unless critical. Use shortest variable names that make sense."
	}
}

func ApplyPonytail(messages []models.Message, level string) []models.Message {
	ponytailPrompt := PonytailPrompt(level)
	var result []models.Message

	foundSystem := false
	for _, msg := range messages {
		if msg.Role == "system" && !foundSystem {
			msg.Content = models.MessageContent(ponytailPrompt + "\n\n" + msg.Content.String())
			foundSystem = true
		}
		if msg.Role == "user" && !foundSystem {
			result = append(result, models.Message{Role: "system", Content: models.MessageContent(ponytailPrompt)})
			foundSystem = true
		}
		result = append(result, msg)
	}

	if !foundSystem {
		result = append([]models.Message{{Role: "system", Content: models.MessageContent(ponytailPrompt)}}, result...)
	}

	return result
}

package agent

// directionsLabel prefixes reviewer directions in a prompt. It tells the agent
// the text is the reviewer's own guidance, not PR content or a PR number.
const directionsLabel = "Reviewer directions (decisions already made or areas to focus on; follow them over the default checklist): "

// DirectionsPrompt is the message sent when directions are given for a review
// whose session already exists, so the resumed agent picks them up.
func DirectionsPrompt(directions string) string {
	if directions == "" {
		return ""
	}
	return directionsLabel + directions
}

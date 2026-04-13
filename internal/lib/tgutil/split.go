package tgutil

// SplitMessage splits s into chunks not exceeding limit bytes,
// preferring to cut at the nearest newline before the limit.
// Preserves newlines in the chunk before the cut point when possible.
// Handles multi-byte UTF-8 characters correctly by respecting rune boundaries.
// If the final segment exceeds the limit, it is recursively split.
func SplitMessage(s string, limit int) []string {
	if len(s) <= limit {
		return []string{s}
	}
	var parts []string
	start := 0
	lastNewline := -1

	for idx, r := range s {
		// Record newline position before checking limit
		if r == '\n' {
			lastNewline = idx
		}

		// Check if we've reached or exceeded the limit
		if idx-start >= limit {
			if lastNewline >= start && lastNewline+1-start <= limit {
				// Include newline in this chunk (if it doesn't exceed limit)
				parts = append(parts, s[start:lastNewline+1])
				start = lastNewline + 1
			} else if lastNewline >= start {
				// Newline exists but including it would exceed limit, cut before it
				parts = append(parts, s[start:lastNewline])
				start = lastNewline
			} else {
				// No newline, cut at current position (start of rune that exceeds limit)
				parts = append(parts, s[start:idx])
				start = idx
			}
			lastNewline = -1
		}
	}

	// Handle remaining portion, recursively split if still too large
	if start < len(s) {
		remaining := s[start:]
		if len(remaining) > limit {
			parts = append(parts, SplitMessage(remaining, limit)...)
		} else {
			parts = append(parts, remaining)
		}
	}

	return parts
}

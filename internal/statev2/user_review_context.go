package statev2

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"unicode/utf8"
)

// UserReviewContext bounds submitted prose while tracking the entire history.
type UserReviewContext struct {
	Messages []string
	Limited  bool
	Digest   string
}

// ReviewUserMessages keeps the most recent user text within maxBytes. It scans
// every retained part for the digest, so edits to omitted messages also invalidate
// an in-flight approval. Missing or ambiguous sources still return an error.
func (d *DB) ReviewUserMessages(ctx context.Context, owner, platform, id string, maxBytes int) (UserReviewContext, error) {
	if maxBytes < 1 {
		return UserReviewContext{}, fmt.Errorf("statev2: invalid user intent byte budget")
	}
	rows, err := d.userMessageRows(ctx, owner, platform, id, -1)
	if err != nil {
		return UserReviewContext{}, err
	}
	defer rows.Close()
	var out UserReviewContext
	hash := sha256.New()
	remaining := maxBytes
	var previous, current string
	clipped := false
	flush := func() {
		if clipped || len(current) > remaining {
			out.Limited = true
		}
		text := userTextTail(current, remaining)
		if text != "" {
			out.Messages = append(out.Messages, text)
			remaining -= len(text)
		}
		// Do not jump over an omitted message to include older context.
		if out.Limited {
			remaining = 0
		}
	}
	for rows.Next() {
		var message, text string
		if err := rows.Scan(&message, &text); err != nil {
			return UserReviewContext{}, err
		}
		_, _ = fmt.Fprintf(hash, "%d:%s%d:%s", len(message), message, len(text), text)
		if message != previous {
			if previous != "" {
				flush()
			}
			previous, current, clipped = message, "", false
		}
		if len(text) > maxBytes-len(current) {
			clipped = true
		}
		if len(text) >= maxBytes {
			current = userTextTail(text, maxBytes)
		} else {
			current = userTextTail(current+text, maxBytes)
		}
	}
	if err := rows.Err(); err != nil {
		return UserReviewContext{}, err
	}
	if previous != "" {
		flush()
	}
	slices.Reverse(out.Messages)
	out.Digest = fmt.Sprintf("%x", hash.Sum(nil))
	return out, nil
}

func userTextTail(text string, maxBytes int) string {
	start := max(0, len(text)-maxBytes)
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}

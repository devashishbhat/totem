package replay

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// lineCoverage is how much of a line a span must cover before the whole line
// is removed instead of just the matched characters. Removing half a sentence
// leaves the prompt ungrammatical, which would itself change the answer.
const lineCoverage = 0.8

// Span is one stretch of a prompt that a memory accounted for:
// [message index, start code point, end code point).
type Span [3]int

// editPayload returns a copy of a Chat Completions request with the given
// spans cut out of its messages. The original payload is not modified.
func editPayload(payload map[string]any, spans []Span) (map[string]any, int, error) {
	clone, err := deepCopy(payload)
	if err != nil {
		return nil, 0, err
	}
	messages, ok := clone["messages"].([]any)
	if !ok {
		return nil, 0, fmt.Errorf("payload has no messages array")
	}

	byMessage := map[int][][2]int{}
	for _, s := range spans {
		idx, start, end := s[0], s[1], s[2]
		if idx < 0 || idx >= len(messages) || end <= start {
			continue
		}
		byMessage[idx] = append(byMessage[idx], [2]int{start, end})
	}

	removed := 0
	for idx, ranges := range byMessage {
		msg, ok := messages[idx].(map[string]any)
		if !ok {
			continue
		}
		content, ok := msg["content"].(string)
		if !ok {
			// Multi-part content is left alone: there is no single offset space
			// to cut from.
			continue
		}
		edited, n := removeSpans(content, ranges)
		msg["content"] = edited
		removed += n
	}
	if removed == 0 {
		return clone, 0, nil
	}
	return clone, removed, nil
}

// removeSpans cuts the given code-point ranges out of one message. A range
// that covers most of its line takes the line with it.
func removeSpans(text string, ranges [][2]int) (string, int) {
	runes := []rune(text)
	starts, ends := lineBounds(runes)

	type cut struct{ start, end int }
	var cuts []cut
	for _, r := range ranges {
		start, end := clampInt(r[0], 0, len(runes)), clampInt(r[1], 0, len(runes))
		if end <= start {
			continue
		}
		line := lineOf(starts, start)
		lineLen := ends[line] - starts[line]
		if lineLen > 0 && float64(end-start) >= lineCoverage*float64(lineLen) {
			// Take the newline too, so no blank line is left behind.
			lineEnd := ends[line]
			if lineEnd < len(runes) && runes[lineEnd] == '\n' {
				lineEnd++
			}
			cuts = append(cuts, cut{starts[line], lineEnd})
			continue
		}
		cuts = append(cuts, cut{start, end})
	}
	if len(cuts) == 0 {
		return text, 0
	}
	// Merge overlaps, then cut from the end so earlier offsets stay valid.
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].start < cuts[j].start })
	merged := []cut{cuts[0]}
	for _, c := range cuts[1:] {
		last := &merged[len(merged)-1]
		if c.start <= last.end {
			if c.end > last.end {
				last.end = c.end
			}
			continue
		}
		merged = append(merged, c)
	}
	for i := len(merged) - 1; i >= 0; i-- {
		runes = append(runes[:merged[i].start], runes[merged[i].end:]...)
	}
	return strings.TrimRight(string(runes), " \t"), len(merged)
}

// lineBounds returns the start and end (exclusive, before the newline) of the
// line each code point belongs to.
func lineBounds(runes []rune) (starts, ends []int) {
	start := 0
	for i, r := range runes {
		if r == '\n' {
			starts = append(starts, start)
			ends = append(ends, i)
			start = i + 1
		}
	}
	starts = append(starts, start)
	ends = append(ends, len(runes))
	return starts, ends
}

func lineOf(starts []int, pos int) int {
	line := 0
	for i, s := range starts {
		if pos >= s {
			line = i
		}
	}
	return line
}

func deepCopy(m map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

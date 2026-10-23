// Package convert turns JSONL chunk records into the support.v1.Corpus protobuf and back.
package convert

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	supportv1 "github.com/twwalsh/bazel-monorepo/proto/support/v1"
)

// Record is one line of the site's chunk export (scripts/support-index.mjs --dump).
type Record struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Text   string `json:"text"`
}

// ToChunk maps a record onto the proto. Whitespace at the ends of text is trimmed; nothing else changes.
func ToChunk(r Record) *supportv1.Chunk {
	return &supportv1.Chunk{
		Id:     r.ID,
		Source: r.Source,
		Title:  r.Title,
		Url:    r.URL,
		Text:   strings.TrimSpace(r.Text),
	}
}

// FromChunk is the inverse of ToChunk for trimmed input.
func FromChunk(c *supportv1.Chunk) Record {
	return Record{ID: c.GetId(), Source: c.GetSource(), Title: c.GetTitle(), URL: c.GetUrl(), Text: c.GetText()}
}

// ReadJSONL parses one JSON object per line. Blank lines are skipped; a malformed line is an error with its
// line number, because a silently dropped chunk is a wrong answer later.
func ReadJSONL(r io.Reader) (*supportv1.Corpus, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	corpus := &supportv1.Corpus{}
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if rec.ID == "" {
			return nil, fmt.Errorf("line %d: chunk has no id", line)
		}
		corpus.Chunks = append(corpus.Chunks, ToChunk(rec))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return corpus, nil
}

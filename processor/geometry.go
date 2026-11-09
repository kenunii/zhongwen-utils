package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode"
)

type Box [4]float64
type Character struct {
	Text string
	Box  Box
	Line int
}
type InputLine struct {
	ID    string   `json:"id"`
	Parts []string `json:"parts"`
}
type Source struct {
	Width, Height float64
	Characters    []Character
	Lines         []InputLine
}
type Word struct {
	Hanzi    string `json:"hanzi"`
	Pinyin   string `json:"pinyin"`
	Meaning  string `json:"meaning"`
	Sentence int    `json:"sentence"`
	Boxes    []Box  `json:"boxes"`
}
type Sentence struct {
	Text        string `json:"text"`
	Translation string `json:"translation"`
}
type Page struct {
	Width     float64    `json:"width"`
	Height    float64    `json:"height"`
	Crop      Box        `json:"crop"`
	Words     []Word     `json:"words"`
	Sentences []Sentence `json:"sentences"`
}
type Answer struct {
	Lines []struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Reason string `json:"correction_reason"`
	} `json:"lines"`
	Sentences []struct {
		Translation string `json:"translation"`
		Words       []struct {
			Text    string `json:"text"`
			Pinyin  string `json:"pinyin"`
			Meaning string `json:"meaning"`
		} `json:"words"`
	} `json:"sentences"`
}

func hasHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
func sameRow(a, b Box) bool { return math.Abs(a[1]+a[3]/2-b[1]-b[3]/2) < math.Min(a[3], b[3])/2 }
func gap(a, b Box) bool     { return sameRow(a, b) && b[0]-a[0]-a[2] > 1.5*math.Max(a[3], b[3]) }
func wordBoxes(chars []Character) []Box {
	boxes := []Box{}
	last := -1
	for _, c := range chars {
		b := c.Box
		if len(boxes) > 0 && c.Line == last && sameRow(boxes[len(boxes)-1], b) && !gap(boxes[len(boxes)-1], b) {
			a := &boxes[len(boxes)-1]
			right := math.Max(a[0]+a[2], b[0]+b[2])
			bottom := math.Max(a[1]+a[3], b[1]+b[3])
			a[0] = math.Min(a[0], b[0])
			a[1] = math.Min(a[1], b[1])
			a[2] = right - a[0]
			a[3] = bottom - a[1]
		} else {
			boxes = append(boxes, b)
			last = c.Line
		}
	}
	return boxes
}
func compile(source Source, raw string) (Page, error) {
	page := Page{Width: source.Width, Height: source.Height, Crop: Box{0, 0, source.Width, source.Height}, Words: []Word{}, Sentences: []Sentence{}}
	var answer Answer
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&answer); err != nil {
		return page, fmt.Errorf("invalid annotation JSON: %w", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return page, fmt.Errorf("trailing annotation JSON")
	}
	if len(answer.Lines) != len(source.Lines) {
		return page, fmt.Errorf("annotation changed the line count")
	}
	corrected := []rune{}
	for i, original := range source.Lines {
		line := answer.Lines[i]
		before := []rune(strings.Join(original.Parts, ""))
		after := []rune(line.Text)
		if line.ID != original.ID || len(before) != len(after) {
			return page, fmt.Errorf("annotation changed source line %s", original.ID)
		}
		changed := false
		for j, a := range before {
			if a != after[j] {
				changed = true
				allowed := false
				for _, group := range confusables {
					if strings.ContainsRune(group, a) && strings.ContainsRune(group, after[j]) {
						allowed = true
					}
				}
				if !allowed {
					return page, fmt.Errorf("unsupported OCR correction on %s", line.ID)
				}
			}
		}
		if changed != (strings.TrimSpace(line.Reason) != "") {
			return page, fmt.Errorf("missing or inconsistent correction reason")
		}
		corrected = append(corrected, after...)
	}
	if len(corrected) != len(source.Characters) {
		return page, fmt.Errorf("character count mismatch")
	}
	cursor := 0
	for si, sentence := range answer.Sentences {
		start := cursor
		if strings.TrimSpace(sentence.Translation) == "" || len(sentence.Words) == 0 {
			return page, fmt.Errorf("empty sentence translation")
		}
		kept := 0
		for wi, word := range sentence.Words {
			count := len([]rune(word.Text))
			end := cursor + count
			// Some responses add sentence-final punctuation not present in the
			// returned OCR lines. Ignore only a standalone, unannotated mark;
			// all remaining tokens must still match the complete source exactly.
			chars := []rune(word.Text)
			if wi == len(sentence.Words)-1 && count == 1 && unicode.IsPunct(chars[0]) &&
				strings.TrimSpace(word.Pinyin) == "" && strings.TrimSpace(word.Meaning) == "" &&
				(cursor == len(corrected) || corrected[cursor] != chars[0]) {
				continue
			}
			if count == 0 || end > len(corrected) || string(corrected[cursor:end]) != word.Text {
				return page, fmt.Errorf("annotation changed or omitted source text at character %d", cursor)
			}
			if hasHan(word.Text) && (strings.TrimSpace(word.Pinyin) == "" || strings.TrimSpace(word.Meaning) == "") {
				return page, fmt.Errorf("missing Chinese word lookup")
			}
			for j := cursor + 1; j < end; j++ {
				if gap(source.Characters[j-1].Box, source.Characters[j].Box) {
					return page, fmt.Errorf("word crosses an exercise blank")
				}
			}
			if strings.TrimSpace(word.Pinyin) != "" {
				page.Words = append(page.Words, Word{word.Text, word.Pinyin, word.Meaning, si, wordBoxes(source.Characters[cursor:end])})
			}
			kept++
			cursor = end
		}
		if kept == 0 {
			return page, fmt.Errorf("empty sentence translation")
		}
		page.Sentences = append(page.Sentences, Sentence{string(corrected[start:cursor]), sentence.Translation})
	}
	if cursor != len(corrected) {
		return page, fmt.Errorf("annotation omitted trailing text")
	}
	return page, nil
}

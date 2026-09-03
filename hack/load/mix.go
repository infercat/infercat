package main

import (
	"fmt"
	"math/rand"
	"strings"
)

// The message mix (ticket 028, founder amendment): a growing conversation. Turn 1 is a ~300-token
// question; the history is append-only (real replies, so an engine's prefix cache sees exactly
// what a real chat gives it); every fifth turn pastes a 4–12K-token document; replies are asked
// for at 300–2,000 tokens. Friend i starts i%seedSpread turns in (synthetic history), so one run
// at any N covers the whole prompt-size range at once. Everything is deterministic per (friend,
// turn) so two runs send the same bytes.

type msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

const tokensPerWord = 1.3 // Gemma's tokenizer on this prose; the engine's prompt_tokens is what gets reported

var vocab = strings.Fields(`the of and to in a is that for it as was with be by on not he this are or his
from at which but have an they one you were all her she there would their we him been has when who will
no more if out so up said what its about than into them can only other time new some could these two may
first then do any like my now over such our man me even most made after also did many before must well
back through years much where your way down should because long each just state those people too how
little good world make very year still see own work men day get here old life both between being under
three never know same last another while us might great since against right came take states used
himself house few use place during without high again home around small however found part thought
school went say once general upon war left every does got united number hand course water until away
always public something fact less though far put head think set called enough almost night end why
eyes find going look asked later knew point next program city business give group toward young days let
room president side social given present several order national possible rather second face per among
form important often things looking early white case become large big need four within felt along
children saw best church ever least power development light thing seemed family interest want members
mind country area others although turned door done open service certain kind problem began different
thus help means sense question`)

// prose returns about n tokens of deterministic pseudo-English for seed.
func prose(seed int64, n int) string {
	r := rand.New(rand.NewSource(seed))
	words := int(float64(n) / tokensPerWord)
	var b strings.Builder
	for i, inSentence := 0, 0; i < words; i++ {
		w := vocab[r.Intn(len(vocab))]
		if inSentence == 0 {
			w = strings.ToUpper(w[:1]) + w[1:]
		}
		b.WriteString(w)
		inSentence++
		if inSentence >= 8+r.Intn(9) || i == words-1 {
			b.WriteString(". ")
			inSentence = 0
			if r.Intn(6) == 0 {
				b.WriteString("\n\n")
			}
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// turnSpec is what a friend sends at one turn: the user message and the reply budget.
type turnSpec struct {
	content   string
	doc       bool
	maxTokens int
}

func turnFor(friend, turn int) turnSpec {
	r := rand.New(rand.NewSource(int64(friend)*1000003 + int64(turn)))
	maxTok := 300 + r.Intn(1701)
	words := maxTok * 3 / 4
	if turn%5 == 0 {
		n := 4000 + r.Intn(8001)
		return turnSpec{doc: true, maxTokens: maxTok, content: fmt.Sprintf(
			"Here is a document I pasted (part %d). Read it, then summarize it and answer the question at the end in about %d words.\n\n%s\n\nQuestion: what are the three main themes, and which one matters most?",
			turn/5, words, prose(r.Int63(), n))}
	}
	return turnSpec{maxTokens: maxTok, content: fmt.Sprintf(
		"Question %d. %sPlease answer at length, in about %d words, with concrete examples.", turn, prose(r.Int63(), 270), words)}
}

// seedHistory is turns 1..k of a conversation that already happened: the questions turnFor would
// have asked, each answered with ~800 tokens of prose. Append-only from here, like a real chat.
func seedHistory(friend, k int) []msg {
	var h []msg
	for t := 1; t <= k; t++ {
		h = append(h, msg{"user", turnFor(friend, t).content}, msg{"assistant", prose(int64(friend)*7919+int64(t), 800)})
	}
	return h
}

// estTokens is the instrument's own estimate of a prompt; the engine's count (usage.prompt_tokens)
// is what every table reports.
func estTokens(h []msg) int {
	n := 0
	for _, m := range h {
		n += int(float64(len(strings.Fields(m.Content)))*tokensPerWord) + 4
	}
	return n
}

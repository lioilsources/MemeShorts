package classify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ol1n/memeshorts/internal/db"
	"github.com/ol1n/memeshorts/internal/llm"
)

const good = `{"format":"before_after","characters":1,"beats":[
 {"who":"A","text":"Fixing the bug","emotion":"Disgusted"},
 {"who":"A","text":"Renaming it to a feature","emotion":"pleased"}],
 "punchline_index":1,"safe_for_brand":true,"needs_context":false}`

func TestParseValid(t *testing.T) {
	r, err := Parse("<think>hmm</think>```json\n" + good + "\n```")
	if err != nil {
		t.Fatal(err)
	}
	if r.Beats[0].Emotion != "disgusted" || r.Skip() != "" {
		t.Errorf("%+v", r)
	}
}

func TestParseInvalid(t *testing.T) {
	cases := map[string]string{
		"format":    strings.Replace(good, `"before_after"`, `"meme"`, 1),
		"who":       strings.Replace(good, `"who":"A"`, `"who":"Z"`, 1),
		"punchline": strings.Replace(good, `"punchline_index":1`, `"punchline_index":5`, 1),
		"roles":     strings.Replace(good, `"who":"A","text":"Fixing`, `"who":"B","text":"Fixing`, 1),
		"unknown":   strings.Replace(good, `"needs_context":false`, `"needs_context":false,"extra":1`, 1),
		"empty":     strings.Replace(good, `"Fixing the bug"`, `"  "`, 1),
		"long":      strings.Replace(good, `"Fixing the bug"`, `"`+strings.Repeat("x", 121)+`"`, 1),
	}
	for name, in := range cases {
		if _, err := Parse(in); err == nil {
			t.Errorf("%s: čekal jsem chybu validace", name)
		}
	}
}

func TestSkip(t *testing.T) {
	r, _ := Parse(good)
	r.SafeForBrand = false
	if r.Skip() == "" {
		t.Error("safe_for_brand=false musí skipnout")
	}
	r.SafeForBrand, r.NeedsContext = true, true
	if r.Skip() == "" {
		t.Error("needs_context=true musí skipnout")
	}
}

func TestFromTextsKnownTemplate(t *testing.T) {
	m := db.Meme{ID: "imgflip:112126428", Title: "Distracted Boyfriend"}
	r, err := FromTexts(m, []string{"New side project", "Me", "The project I started yesterday"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "dialog" || r.Characters != 3 || r.Beats[1].Who != "A" || r.Beats[2].Who != "B" || r.PunchlineIndex != 2 {
		t.Errorf("%+v", r)
	}
}

func TestFromTextsUnknownTemplate(t *testing.T) {
	r, err := FromTexts(db.Meme{ID: "reddit:x"}, []string{"a", "b"})
	if err != nil || r.Format != "before_after" {
		t.Fatalf("%+v %v", r, err)
	}
}

// LLM vrátí napoprvé nevalidní JSON, podruhé opravený — Classifier musí zkusit znovu s chybou.
func TestClassifyRetriesWithValidationError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("CF-Access-Client-Id") != "id" {
			w.WriteHeader(403)
			return
		}
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		content := strings.Replace(good, `"before_after"`, `"nope"`, 1)
		if calls == 2 {
			if last := req.Messages[len(req.Messages)-1].Content; !strings.Contains(last, "format") {
				t.Errorf("retry neobsahuje chybu validace: %q", last)
			}
			content = good
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": content}}}})
	}))
	defer srv.Close()
	c := &Classifier{LLM: llm.New(srv.URL, "m", "", "id", "sec")}
	r, err := c.Classify(context.Background(), db.Meme{ID: "imgflip:181913649", Title: "Drake Hotline Bling", BoxCount: 2}, nil)
	if err != nil || calls != 2 || r.Format != "before_after" {
		t.Fatalf("r=%+v err=%v calls=%d", r, err, calls)
	}
}

func TestClassifyOfflineNeedsTexts(t *testing.T) {
	c := &Classifier{}
	if _, err := c.Classify(context.Background(), db.Meme{ID: "imgflip:1"}, nil); err == nil {
		t.Fatal("offline bez textů má selhat")
	}
	if _, err := c.Classify(context.Background(), db.Meme{ID: "imgflip:1"}, []string{"x"}); err != nil {
		t.Fatal(err)
	}
}

func TestKnownTemplatesConsistent(t *testing.T) {
	for id, tpl := range knownTemplates {
		if !contains(Formats, tpl.Format) || len(tpl.Roles) == 0 || tpl.Hint == "" {
			t.Errorf("%s: neúplná šablona", id)
		}
		for _, r := range tpl.Roles {
			if !whoRe.MatchString(r.Who) {
				t.Errorf("%s: role %q", id, r.Who)
			}
		}
	}
}

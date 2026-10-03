package classify

import (
	"fmt"
	"strings"

	"github.com/ol1n/memeshorts/internal/db"
)

const systemPrompt = `You turn meme FORMATS into short vertical videos starring our own original anime characters.
We never reuse the original image, real people or film characters — only the joke structure.

Return ONLY a JSON object with exactly these keys:
{
  "format": "reaction" | "dialog" | "before_after" | "text_over" | "panels" | "unknown",
  "characters": 1-4,                      // distinct roles used in beats
  "beats": [ {"who": "A"|"B"|"C"|"D", "text": "caption, max 120 chars", "emotion": "one word, e.g. smug"} ],  // 1-6 beats, in playback order
  "punchline_index": 0-based index of the beat that lands the joke (usually the last),
  "safe_for_brand": true|false,          // false for politics, tragedy, slurs, sexual content, real-person mockery
  "needs_context": true|false            // true if the joke only works if you know a specific film/person/news event
}

Rules:
- Captions are short, punchy, internet-native English. No hashtags, no emojis.
- Universal relatable topics (work, school, gaming, coding, food, sleep, procrastination) beat niche ones.
- If the template's humour depends on the real person in it, rewrite the joke so it works with a generic character, or set needs_context=true.
- Do not mention the original template, film, or person in captions.`

func userPrompt(m db.Meme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Meme source: %s\nTemplate name: %s\nText boxes: %d\n", m.Source, m.Title, m.BoxCount)
	if t, ok := KnownTemplate(m.ID); ok {
		fmt.Fprintf(&b, "Known format: %s\nHow the joke works: %s\nRoles per box: ", t.Format, t.Hint)
		for i, r := range t.Roles {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "box%d=%s(%s)", i+1, r.Who, r.Emotion)
		}
		b.WriteString("\n")
	}
	if len(m.Texts) > 0 {
		b.WriteString("Existing captions (OCR) — keep their meaning, split into beats:\n")
		for i, t := range m.Texts {
			fmt.Fprintf(&b, "%d. %s\n", i+1, t)
		}
	} else {
		b.WriteString("This is a blank template: write ONE fresh, original joke in this format.\n")
	}
	return b.String()
}

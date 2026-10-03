package classify

// Role je slot v šabloně: kdo mluví a s jakou emocí.
type Role struct{ Who, Emotion string }

// Template popisuje strukturu známé šablony nezávisle na jejím obrázku.
// Formát memu není chráněný, obraz ano — proto zde žádné odkazy na originál.
type Template struct {
	Format string
	Roles  []Role // i-tý text → Roles[min(i, len-1)]
	Hint   string // popis mechaniky vtipu pro LLM
}

var knownTemplates = map[string]Template{
	"imgflip:181913649": {"before_after", []Role{{"A", "disgusted"}, {"A", "pleased"}},
		"one character rejects option 1 (top), then happily approves option 2 (bottom)"},
	"imgflip:87743020": {"dialog", []Role{{"A", "nervous"}, {"A", "nervous"}, {"A", "panicked"}},
		"character sweats choosing between two buttons (texts 1,2); text 3 labels the character"},
	// Pořadí boxů na Imgflipu: nová věc, subjekt, zanedbaná věc.
	"imgflip:112126428": {"dialog", []Role{{"C", "flirty"}, {"A", "distracted"}, {"B", "shocked"}},
		"box1 = C the new tempting thing, box2 = A the subject who is distracted, box3 = B the current thing being neglected"},
	"imgflip:217743513": {"before_after", []Role{{"A", "smug"}, {"A", "stubborn"}},
		"card says 'do X or draw 25'; character would rather draw 25 than do X"},
	"imgflip:124822590": {"before_after", []Role{{"A", "calm"}, {"A", "reckless"}, {"A", "reckless"}},
		"sensible straight road vs. exit ramp; car swerves to the worse option"},
	"imgflip:252600902": {"dialog", []Role{{"A", "shocked"}, {"B", "menacing"}},
		"A realises something ('wait, it's all X?'), B: 'always has been'"},
	"imgflip:135256802": {"dialog", []Role{{"A", "confident"}, {"B", "confident"}, {"A", "proud"}},
		"two different groups shake hands over a shared trait"},
	"imgflip:131940431": {"panels", []Role{{"A", "scheming"}, {"A", "scheming"}, {"A", "confused"}, {"A", "horrified"}},
		"four-step plan, step 3 has a flaw, step 4 character re-reads it in horror"},
	"imgflip:97984": {"reaction", []Role{{"A", "smug"}, {"A", "smug"}},
		"small character smirks at the camera while chaos happens behind"},
	"imgflip:4087833": {"reaction", []Role{{"A", "exhausted"}, {"A", "exhausted"}},
		"character has been waiting forever for something that never comes"},
	"imgflip:309868304": {"dialog", []Role{{"A", "businesslike"}, {"A", "businesslike"}, {"A", "smug"}},
		"'trade offer: I receive X, you receive Y' — unfair deal"},
	"imgflip:129242436": {"text_over", []Role{{"A", "smug"}, {"A", "smug"}},
		"character sits at a table with a sign stating a hot take: 'X. change my mind'"},
	"imgflip:438680": {"dialog", []Role{{"A", "naive"}, {"B", "angry"}},
		"A starts saying something dumb, B slaps them mid-sentence with the correction"},
	"imgflip:188390779": {"dialog", []Role{{"A", "furious"}, {"B", "confused"}},
		"A yells an accusation, B sits calmly with a deadpan reply"},
	"imgflip:102156234": {"text_over", []Role{{"A", "mocking"}, {"A", "mocking"}},
		"character repeats a statement in mocking alternating caps"},
	"imgflip:180190441": {"dialog", []Role{{"B", "neutral"}, {"B", "neutral"}, {"A", "unimpressed"}},
		"A is shown two things and says 'they're the same picture'"},
	"imgflip:100777631": {"reaction", []Role{{"A", "confident"}, {"A", "confused"}, {"A", "confident"}},
		"character confidently misidentifies thing: 'is this a X?'"},
	"imgflip:247375501": {"before_after", []Role{{"A", "strong"}, {"B", "weak"}, {"A", "strong"}, {"B", "weak"}},
		"strong past version vs. weak present version, each with a caption"},
	"imgflip:93895088": {"panels", []Role{{"A", "dull"}, {"A", "enlightened"}, {"A", "enlightened"}, {"A", "transcendent"}},
		"four escalating levels of 'galaxy brain' ideas, increasingly absurd"},
	"imgflip:178591752": {"before_after", []Role{{"A", "plain"}, {"A", "fancy"}},
		"same thing said plainly, then said fancily"},
	"imgflip:370867422": {"reaction", []Role{{"A", "sneaky"}, {"A", "sneaky"}},
		"character peeks in: 'no X?' — mocking absence"},
}

func KnownTemplate(id string) (Template, bool) {
	t, ok := knownTemplates[id]
	return t, ok
}

// guessTemplate — bez znalosti šablony: 1 text = reaction, 2 = before/after, víc = dialog.
func guessTemplate(n int) Template {
	switch {
	case n <= 1:
		return Template{Format: "reaction", Roles: []Role{{"A", "smug"}}}
	case n == 2:
		return Template{Format: "before_after", Roles: []Role{{"A", "unimpressed"}, {"A", "pleased"}}}
	default:
		return Template{Format: "dialog", Roles: []Role{{"A", "neutral"}, {"B", "neutral"}, {"A", "smug"}}}
	}
}

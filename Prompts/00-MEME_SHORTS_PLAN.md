# MEME_SHORTS_PLAN.md — Rozhýbání statických memů do shorts videí

Handoff pro Claude Code. Cíl: automatická pipeline, která z meme šablon / trendujících memů vyrobí krátká vertikální videa (6–15 s, 9:16) s **vlastními postavami**, hotová k uploadu na YouTube Shorts / TikTok / Reels.

Kontext: běží na DGX Spark, generace přes `comfyui.ol1n.com`, LLM přes `llm.ol1n.com`, job queue a gateway v AiStacku, karty v Tsumiki stylu. Postavy = Ol1nova anime postavy (viz anime-story-videos).

---

## 0. Rozhodnutí (fixní)

| Oblast | Rozhodnutí | Důvod |
|---|---|---|
| Zdroj memů | Imgflip API (šablony) + Reddit API (trendy) | Zdarma, strukturované, bez scrapingu |
| Vlastní obsah | Vždy regenerovat obraz s vlastní postavou (flux-schnell), originál = jen storyboard | Cizí IP (filmy, fotky lidí) → nemonetizovatelné; formát memu chráněn není |
| Video model | Wan 2.2 I2V 5B (rychlý) / 14B (kvalita) přes ComfyUI | Už nasazeno pro dance/anime pipeline |
| TTS | Kokoro (EN/CZ fallback XTTS) | Lehké, lokální |
| Kompozice | ffmpeg (ne MoviePy) | Rychlost, méně závislostí |
| Výstup | 1080×1920, H.264, 30 fps, AAC, max 15 s | Shorts/TikTok limity |
| Jazyk titulků | EN default, CZ volitelně | Dosah |
| Audio | Jen TTS + royalty-free SFX/hudba z vlastní knihovny | Licenční čistota |
| AI label | Metadata + vizuální „AI generated" badge | EU AI Act |

---

## 1. Architektura

```
services/memeshorts/           (Go, nový service v AiStacku)
├── cmd/memeshorts/main.go
├── internal/
│   ├── ingest/        Imgflip + Reddit fetchery → SQLite
│   ├── classify/      LLM klasifikace formátu + extrakce textů
│   ├── storyboard/    formát → seznam scén (JSON)
│   ├── recipes/       animační recepty (kenburns, i2v, dialog, panels)
│   ├── compose/       ffmpeg render 9:16 + titulky + audio
│   ├── publish/       YouTube Data API, TikTok Content Posting API
│   └── metrics/       zpětný sběr views/retention → Discord bot
├── workflows/         ComfyUI JSON workflowy (I2V, flux-schnell, TTS)
├── assets/            fonty, SFX, hudba, postavy (ref obrázky)
└── data/memes.db      SQLite + WAL
```

Job flow (AiStack job queue, card `meme-short`):
`ingest → classify → storyboard → generate(images) → animate(recipe) → compose → qa → publish → metrics`

Každý stage idempotentní, výstupy na disku pod `jobs/<id>/<stage>/`, resume dle manifestu.

---

## 2. Datový model (SQLite)

```sql
CREATE TABLE memes (
  id TEXT PRIMARY KEY,          -- imgflip:<id> | reddit:<id>
  source TEXT, title TEXT, image_url TEXT, local_path TEXT,
  box_count INT, texts JSON,    -- extrahované texty
  format TEXT,                  -- reaction|dialog|before_after|text_over|panels|unknown
  score REAL, fetched_at INT,
  rights TEXT                   -- 'template-only' (nikdy nepublikovat originál)
);
CREATE TABLE jobs (
  id TEXT PRIMARY KEY, meme_id TEXT, recipe TEXT, status TEXT,
  storyboard JSON, outputs JSON, metrics JSON,
  created_at INT, updated_at INT
);
```

---

## 3. Stage detaily

### 3.1 Ingest
- Imgflip: `GET https://api.imgflip.com/get_memes` → ~100 šablon, `box_count`, obrázek. Cron 1×/týden.
- Reddit: OAuth app, `r/memes`, `r/dankmemes`, `r/wholesomememes` `/top?t=day`, limit 50. Cron 1×/den. Rate 1 req/s.
- Volitelně později: memegen.link šablony (GitHub repo), Know Your Meme jen jako ruční referenční zdroj (bez scrapingu).
- Originál obrázek ukládat jen lokálně pro analýzu; nikdy do výstupu.

### 3.2 Classify (LLM, llm.ol1n.com)
Vision/OCR (ocr přes existující HTR service nebo PaddleOCR) → texty. Prompt vrací JSON:
```json
{"format":"dialog","characters":2,"beats":[{"who":"A","text":"...","emotion":"smug"},...],
 "punchline_index":1,"safe_for_brand":true,"needs_context":false}
```
`safe_for_brand=false` nebo `needs_context=true` (vyžaduje znalost konkrétního filmu/osoby) → skip.

### 3.3 Storyboard
Formát → scény. Každá scéna: `{character, pose_prompt, emotion, text, duration_ms, camera}`.
Mapování známých šablon na vlastní postavy je v `assets/characters/map.yaml` (např. Drake → postava „Kiro", Distracted Boyfriend → trojice).

### 3.4 Generate (flux-schnell přes ComfyUI)
Pro každou scénu vygenerovat obrázek vlastní postavy v pozici ze storyboardu. Identity přes referenční obrázek (IP-Adapter / existující face-avatar pipeline). 1024×1024, poté crop na 9:16 kompozici.

### 3.5 Animate — recepty
| Recept | Kdy | Technika | Cena |
|---|---|---|---|
| `kenburns` | before/after, text_over, Drake | ffmpeg zoompan + text pop-in | ~1 s CPU |
| `i2v` | reaction, portrét | Wan 2.2 I2V 5B, 2–4 s klip, prompt „subtle motion, blink, breathing" | ~30–60 s GPU |
| `dialog` | dialog 2 postav | 2× i2v + TTS per replika, střih podle délky audia | ~2 min GPU |
| `panels` | panel comic | sekvence kenburns/i2v panelů, wipe přechody | dle počtu |

M1 jede jen `kenburns` (ověření celé roury bez difuze). Difuzní recepty od M2.

### 3.6 Compose (ffmpeg)
- Plátno 1080×1920, pozadí blur verze scény.
- Titulky: bold font (Inter Black/Impact alt), bílý text + černý stroke, animace pop-in (drawtext + enable=between).
- Hook: první 0.8 s = punchline teaser nebo rychlý zoom.
- Audio: TTS tracks + SFX (vine boom, record scratch…) + tichý hudební podkres, loudness -14 LUFS.
- Vypálený AI badge v rohu + metadata.

### 3.7 QA
Automatické checky: délka 5–15 s, žádný black frame, audio přítomné, OCR titulků = storyboard texty (chybné rendery zahodit). Volitelně LLM vision „is the joke readable?" score ≥ 7/10.

### 3.8 Publish
- YouTube Data API v3 (`videos.insert`, `#Shorts` v title/desc).
- TikTok Content Posting API (vyžaduje app review — začít žádost hned v M0).
- Instagram Reels přes Graph API (business účet) — až M4.
- Metadata: title, hashtagy z LLM, `madeForKids=false`, AI disclosure.

### 3.9 Metrics
Denně stáhnout views, avg view duration, likes → `jobs.metrics`. Posílat do Discord botu (#metrics). Po 50+ videích A/B: hook typ, recept, délka.

---

## 4. Milníky

- **M0** – 5 shortů ručně (ComfyUI + ffmpeg), 3 recepty, nahrát, změřit baseline retenci. Zažádat o TikTok API. 
- **M1** – Ingest + classify + storyboard + `kenburns` + compose end-to-end automaticky. 1 příkaz = 1 short.
- **M2** – `i2v` recept, identity postav stabilní (≥ 80 % úspěšnost dle QA).
- **M3** – `dialog` recept s TTS; denní batch 10 shortů z Reddit trendů; publish na YouTube automaticky.
- **M4** – Metrics loop, A/B hooků, TikTok + Reels publish, Discord reporty.
- **M5** – Veřejná verze jako Tsumiki karta: uživatel pošle mem / text → dostane short (monetizace kreditů jako u dance).

---

## 5. Otevřené otázky

1. Postavy: použít existující anime sadu, nebo vytvořit 3–5 „meme" postav s jasnými archetypy (smug, confused, hyped)?
2. Jazyk primárního kanálu — EN globální, nebo CZ niche (menší konkurence, menší dosah)?
3. Reddit API: osobní OAuth app stačí pro read-only top posty? Ověřit aktuální limity.
4. TTS hlasy: jeden hlas per postava — Kokoro má omezený výběr; stojí za to XTTS klon?
5. Watermark/branding: logo kanálu vs. čistý obraz pro algoritmus?
6. Kde běží cron (Spark vs. VPS) — publish nemusí být na GPU.

---

## 6. První kroky pro Claude Code

1. Založit `services/memeshorts` v AiStacku, skeleton dle sekce 1, SQLite migrace.
2. `ingest`: Imgflip fetcher + test, Reddit fetcher za feature flagem.
3. `classify`: prompt + JSON schema validace, 20 šablon ručně ověřit.
4. `recipes/kenburns` + `compose`: z 1 obrázku + 2 textů udělat validní 9:16 MP4.
5. CLI: `memeshorts run --meme imgflip:181913649 --recipe kenburns`.

# MemeShorts

## Účel
Go CLI `memeshorts`: z meme šablon dělá 6–15s vertikální shorty (1080×1920) s vlastními postavami.
Originál memu je jen předloha struktury vtipu, do výstupu se nedostane.

## Build a test
```
make build        # go build -o bin/memeshorts ./cmd/memeshorts
make test         # go test ./... (e2e test potřebuje ffmpeg)
make test-short   # go test -short ./... bez ffmpeg
```
Gate před pushem: `make build && make test-short` (s ffmpeg `make test`).

## Mapa
- `cmd/memeshorts/` — CLI: `ingest`, `list`, `classify`, `run`, `library`, `lab`.
- `internal/pipeline/` — roura `classify → storyboard → generate → animate → compose → qa`, stav v `jobs/<id>/`.
- `internal/{classify,storyboard,generate,recipes,compose,qa}/` — jednotlivé stage.
- `internal/{llm,comfy,spark}/` — klienti LLM, ComfyUI a rozvrh oken SPARKu.
- `internal/library/`, `internal/lab/` — knihovna postav × emocí, G0/G1 experimenty.
- `internal/config/` — konfigurace z env (`.env`, vzor `.env.example`).
- `assets/` — postavy (`characters/*.yaml`), font, hudba; `workflows/` — ComfyUI workflow JSON.
- `Prompts/` — plány (`00-MEME_SHORTS_PLAN.md`, `01-GRAPHICS_PLAN.md`).

## Invarianty
- Klíč stage = hash jeho voleb + klíč předchozí stage; opakovaný běh se nepřepočítává.
- Mimo okna SPARKu (`internal/spark/windows.go`) klient na SPARK nic neposílá.
- S `--text` a bez schválené knihovny postav roura doběhne s placeholdery i offline.
- Adresy SPARKu jen v `.env`, ne v dokumentaci (veřejné repo).

## Kam dál
README (rychlý start, knihovna postav, stage), `Prompts/`.
